package component

import (
	"context"
	"fmt"
	"sync"

	"game-server/internal/config"
	"game-server/internal/dashboard"
	"game-server/internal/gameerror"
	"game-server/internal/models"
	"game-server/protocol"

	"github.com/google/uuid"
	"github.com/topfreegames/pitaya/v2"
	"github.com/topfreegames/pitaya/v2/component"
	"github.com/topfreegames/pitaya/v2/constants"
	"github.com/topfreegames/pitaya/v2/logger"
)

type Room struct {
	component.Base
	app     pitaya.Pitaya
	rooms   map[string]*models.GameRoom
	players map[string]*models.Player // Global UID -> Player (for quick check)
	events  []string
	mu      sync.RWMutex // Protects rooms, players maps and events
}

func NewRoom(app pitaya.Pitaya) *Room {
	return &Room{
		app:     app,
		rooms:   make(map[string]*models.GameRoom),
		players: make(map[string]*models.Player),
		events:  make([]string, 0, 10),
	}
}

func (r *Room) LogEvent(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) >= 10 {
		r.events = r.events[1:]
	}
	r.events = append(r.events, msg)
}

func (r *Room) GetDashboardData() dashboard.Data {
	r.mu.RLock()
	// Snapshot global state to minimize lock contention
	totalPlayers := len(r.players)
	roomSnapshot := make([]*models.GameRoom, 0, len(r.rooms))
	for _, room := range r.rooms {
		roomSnapshot = append(roomSnapshot, room)
	}
	eventsSnapshot := make([]string, len(r.events))
	copy(eventsSnapshot, r.events)
	r.mu.RUnlock()

	data := dashboard.Data{
		TotalRooms:   len(roomSnapshot),
		TotalPlayers: totalPlayers,
		Rooms:        make([]dashboard.RoomInfo, 0, len(roomSnapshot)),
		Events:       make([]string, len(eventsSnapshot)),
	}

	for _, room := range roomSnapshot {
		players := room.GetPlayers()
		pInfos := make([]dashboard.PlayerInfo, 0, len(players))
		for _, p := range players {
			pInfos = append(pInfos, dashboard.PlayerInfo{
				Name: p.Name,
				X:    p.Position.X,
				Y:    p.Position.Y,
				Z:    p.Position.Z,
			})
		}

		data.Rooms = append(data.Rooms, dashboard.RoomInfo{
			ID:          room.ID,
			Name:        room.Name,
			PlayerCount: len(players),
			MaxPlayers:  room.MaxPlayers,
			Players:     pInfos,
		})
	}

	// Copy events in reverse order (newest first)
	for i, e := range eventsSnapshot {
		data.Events[len(eventsSnapshot)-1-i] = e
	}

	return data
}

func (r *Room) Init() {
	// Create a default room for quick testing
	r.createRoom("Lobby", 100)
}

func (r *Room) createRoom(name string, maxPlayers int) *models.GameRoom {
	id := uuid.New().String()
	if name == "Lobby" {
		id = "lobby"
	}

	room := models.NewGameRoom(id, name, maxPlayers)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.rooms[id] = room

	// Create Pitaya Group
	err := r.app.GroupCreate(context.Background(), id)
	if err != nil && err != constants.ErrGroupAlreadyExists {
		logger.Log.Errorf("Failed to create group %s: %v", id, err)
	}

	logger.Log.Infof("Created room: %s (%s)", name, id)
	return room
}

// Create Room Handler
func (r *Room) Create(ctx context.Context, req *protocol.CreateRoomRequest) (*protocol.CreateRoomResponse, error) {
	if req.Name == "" {
		return nil, gameerror.New(gameerror.CodeInvalidRequest, "room name cannot be empty")
	}
	max := int(req.MaxPlayers)
	if max <= 0 {
		max = 10
	}

	room := r.createRoom(req.Name, max)
	return &protocol.CreateRoomResponse{
		Id:   room.ID,
		Name: room.Name,
	}, nil
}

// List Rooms Handler
func (r *Room) List(ctx context.Context, req *protocol.ListRoomsRequest) (*protocol.ListRoomsResponse, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := &protocol.ListRoomsResponse{
		Rooms: make([]*protocol.RoomInfo, 0, len(r.rooms)),
	}

	for _, room := range r.rooms {
		res.Rooms = append(res.Rooms, &protocol.RoomInfo{
			Id:    room.ID,
			Name:  room.Name,
			Count: int32(room.PlayerCount()),
			Max:   int32(room.MaxPlayers),
		})
	}

	return res, nil
}

// Join Room Handler
func (r *Room) Join(ctx context.Context, req *protocol.JoinRequest) (*protocol.JoinResponse, error) {
	s := r.app.GetSessionFromCtx(ctx)
	uid := fmt.Sprintf("%d", s.ID())

	if s.UID() == "" {
		if err := s.Bind(ctx, uid); err != nil && err != constants.ErrSessionAlreadyBound {
			return nil, gameerror.New(gameerror.CodeInternalError, err.Error())
		}
	}

	r.mu.Lock()
	if _, exists := r.players[uid]; exists {
		// Player is already in a room. Check if we can auto-leave.
		r.mu.Unlock() // Unlock to avoid deadlock in onPlayerDisconnect

		// Get current room ID from session
		oldRoomVal := s.Get("roomID")
		if oldRoomID, ok := oldRoomVal.(string); ok && oldRoomID != "" {
			if oldRoomID == req.RoomId {
				return nil, gameerror.New(gameerror.CodeAlreadyInRoom, "player already in this room")
			}
			// Auto-leave the old room
			logger.Log.Infof("Player %s switching from room %s to %s", uid, oldRoomID, req.RoomId)
			r.onPlayerDisconnect(uid, oldRoomID)
		} else {
			// Inconsistent state: r.players has uid but session has no roomID.
			// Force clean up r.players
			r.mu.Lock()
			delete(r.players, uid)
			r.mu.Unlock()
		}

		// Re-acquire lock to check again (though we just cleared it, another request could have come in)
		r.mu.Lock()
		if _, exists := r.players[uid]; exists {
			r.mu.Unlock()
			return nil, gameerror.New(gameerror.CodeAlreadyInRoom, "player already in a room (race condition)")
		}
	}

	// Important: Lock is held here if we reached this point

	roomID := req.RoomId
	if roomID == "" {
		roomID = "lobby"
	}

	room, exists := r.rooms[roomID]
	if !exists {
		// Try to find room by name if ID lookup fails
		for _, v := range r.rooms {
			if v.Name == roomID {
				room = v
				exists = true
				roomID = v.ID // Update roomID to correct UUID
				break
			}
		}
	}

	if !exists {
		r.mu.Unlock()
		return nil, gameerror.New(gameerror.CodeRoomNotFound, fmt.Sprintf("room not found: %s", roomID))
	}

	player := models.NewPlayer(uid, req.Name)
	r.players[uid] = player
	r.mu.Unlock()

	// Add to Room Model (Handles Room Lock)
	if err := room.AddPlayer(player); err != nil {
		// Rollback Global
		r.mu.Lock()
		delete(r.players, uid)
		r.mu.Unlock()
		return nil, gameerror.New(gameerror.CodeRoomFull, err.Error())
	}

	// IO: Add to Pitaya Group
	if err := r.app.GroupAddMember(ctx, roomID, uid); err != nil {
		logger.Log.Errorf("Failed to add member to group: %v", err)
		// Rollback
		room.RemovePlayer(uid)
		r.mu.Lock()
		delete(r.players, uid)
		r.mu.Unlock()
		return nil, gameerror.New(gameerror.CodeInternalError, "failed to join room group")
	}

	s.Set("roomID", roomID)

	// Broadcast Join to AOI Neighbors only
	// NOTE: AOI Logic handles the broadcast, but we need to make sure the player is in AOI first.
	// AddPlayer called r.AOI.Enter(), so they are in.

	neighbors, _ := room.AOI.GetNeighbors(uid)
	if len(neighbors) > 0 {
		r.app.SendPushToUsers("OnPlayerJoin", &protocol.PlayerJoinPush{
			Id:   player.ID,
			Name: player.Name,
		}, neighbors, config.Conf.Server.Type)
	}

	// NEW: Also send 'OnPlayerEnterAOI' to the joining player for all existing neighbors!
	// Otherwise the new player sees nobody until they move.
	// Since AOI.GetNeighbors returns existing entities, we can reuse it.
	for _, neighborID := range neighbors {
		neighbor := room.GetPlayer(neighborID)
		if neighbor != nil {
			r.app.SendPushToUsers("OnPlayerEnterAOI", &protocol.PlayerState{
				Id:       neighbor.ID,
				Position: neighbor.Position,
				Rotation: neighbor.Rotation,
			}, []string{uid}, config.Conf.Server.Type)
		}
	}

	s.OnClose(func() {
		// Read current roomID from session instead of using closure-captured value
		val := s.Get("roomID")
		if currentRoomID, ok := val.(string); ok && currentRoomID != "" {
			r.onPlayerDisconnect(uid, currentRoomID)
		}
	})

	logger.Log.Infof("Player %s joined room %s", uid, roomID)
	r.LogEvent("Player %s (%s) joined room %s", player.Name, uid, roomID)

	return &protocol.JoinResponse{
		Code:    gameerror.CodeOK,
		Message: "Joined successfully",
		RoomId:  roomID,
	}, nil
}

func (r *Room) onPlayerDisconnect(uid, roomID string) {
	r.mu.Lock()
	delete(r.players, uid)
	room, exists := r.rooms[roomID]
	r.mu.Unlock()

	if !exists {
		return
	}

	// Use Model method
	if room.HasPlayer(uid) {
		// Get neighbors before removing for broadcast
		neighbors, _ := room.AOI.GetNeighbors(uid)

		room.RemovePlayer(uid)

		ctx := context.Background()
		r.app.GroupRemoveMember(ctx, roomID, uid)

		// Broadcast to AOI neighbors that player left
		if len(neighbors) > 0 {
			r.app.SendPushToUsers("OnPlayerLeave", &protocol.PlayerLeavePush{
				Id: uid,
			}, neighbors, config.Conf.Server.Type)
		}

		// ALSO Broadcast to the ENTIRE ROOM group about player count change
		// This is for TUI /list or any global count listeners
		r.app.GroupBroadcast(context.Background(), config.Conf.Server.Type, roomID, "OnPlayerLeave", &protocol.PlayerLeavePush{
			Id: uid,
		})

		logger.Log.Infof("Player %s left room %s", uid, roomID)
		r.LogEvent("Player %s left room %s", uid, roomID)
	}
}

// Move Handler (Request)
func (r *Room) Move(ctx context.Context, req *protocol.MoveRequest) (*protocol.MoveResponse, error) {
	s := r.app.GetSessionFromCtx(ctx)
	uid := s.UID()
	if uid == "" {
		logger.Log.Warnf("Move failed: UID is empty")
		return nil, gameerror.New(gameerror.CodeUnauthorized, "not authenticated")
	}

	val := s.Get("roomID")
	if val == nil {
		logger.Log.Warnf("Move failed: player %s not in any room", uid)
		return nil, gameerror.New(gameerror.CodeNotInRoom, "not in any room")
	}
	roomID, ok := val.(string)
	if !ok || roomID == "" {
		logger.Log.Warnf("Move failed: invalid roomID for player %s", uid)
		return nil, gameerror.New(gameerror.CodeNotInRoom, "not in any room")
	}

	r.mu.RLock()
	room, exists := r.rooms[roomID]
	r.mu.RUnlock()

	if !exists {
		logger.Log.Warnf("Move failed: room %s not found for player %s", roomID, uid)
		return nil, gameerror.New(gameerror.CodeRoomNotFound, "room not found")
	}

	// Get Player from Room Model
	player := room.GetPlayer(uid)
	if player == nil {
		logger.Log.Warnf("Move failed: player %s not found in room %s", uid, roomID)
		return nil, gameerror.New(gameerror.CodePlayerNotFound, "player not found in room")
	}

	// Update Player State via Model (Validation included)
	if err := player.UpdatePosition(req.Position, req.Rotation); err != nil {
		// Validation failed! Return error response
		logger.Log.Warnf("Invalid move from %s: %v", uid, err)
		return &protocol.MoveResponse{
			Code:     400,
			Message:  fmt.Sprintf("Invalid move: %v", err),
			Position: player.Position, // Return last valid position
		}, nil
	}

	// Calculate AOI: Get entities that need to be notified
	// NOTE: We only notify neighbors about the move.
	// We don't need to handle Enter/Leave AOI here because Move() returns leave/enter lists
	// But for simple movement sync, we just broadcast to neighbors.
	// Wait, Move() returns leave/enter lists which we should handle!

	leaveIDs, enterIDs, err := room.AOI.Move(uid, req.Position)
	if err != nil {
		logger.Log.Errorf("AOI Move failed: %v", err)
		return nil, gameerror.New(gameerror.CodeInternalError, fmt.Sprintf("AOI move failed: %v", err))
	}

	// 1. Handle AOI Enter/Leave events
	// Notify 'enterIDs' that 'uid' has entered their view
	if len(enterIDs) > 0 {
		r.app.SendPushToUsers("OnPlayerEnterAOI", &protocol.PlayerState{
			Id:       player.ID,
			Position: player.Position,
			Rotation: player.Rotation,
		}, enterIDs, config.Conf.Server.Type)

		// Notify 'uid' about 'enterIDs' (they entered my view)
		for _, eid := range enterIDs {
			otherPlayer := room.GetPlayer(eid)
			if otherPlayer != nil {
				r.app.SendPushToUsers("OnPlayerEnterAOI", &protocol.PlayerState{
					Id:       otherPlayer.ID,
					Position: otherPlayer.Position,
					Rotation: otherPlayer.Rotation,
				}, []string{uid}, config.Conf.Server.Type)
			}
		}
	}

	// Notify 'leaveIDs' that 'uid' has left their view
	if len(leaveIDs) > 0 {
		r.app.SendPushToUsers("OnPlayerLeaveAOI", &protocol.PlayerLeavePush{
			Id: uid,
		}, leaveIDs, config.Conf.Server.Type)

		// Notify 'uid' about 'leaveIDs' (they left my view)
		for _, eid := range leaveIDs {
			r.app.SendPushToUsers("OnPlayerLeaveAOI", &protocol.PlayerLeavePush{
				Id: eid,
			}, []string{uid}, config.Conf.Server.Type)
		}
	}

	// 2. Broadcast movement to current neighbors (AOI)
	// Instead of GroupBroadcast (which is everyone in room), we use AOI neighbors
	neighbors, _ := room.AOI.GetNeighbors(uid)
	if len(neighbors) > 0 {
		r.app.SendPushToUsers("OnPlayerMove", &protocol.PlayerMovePush{
			Id:       uid,
			Position: req.Position,
			Rotation: req.Rotation,
		}, neighbors, config.Conf.Server.Type)
	}

	logger.Log.Debugf("Player %s moved to: %v. Neighbors: %d", uid, req.Position, len(neighbors))

	// Return success response
	return &protocol.MoveResponse{
		Code:     200,
		Message:  "Move successful",
		Position: req.Position,
	}, nil
}

// Message Handler (Notify) - Chat
func (r *Room) Message(ctx context.Context, msg *protocol.ChatMessage) {
	s := r.app.GetSessionFromCtx(ctx)
	uid := s.UID()

	val := s.Get("roomID")
	if val == nil {
		return
	}
	roomID, ok := val.(string)

	if !ok || roomID == "" {
		return
	}

	r.app.GroupBroadcast(ctx, config.Conf.Server.Type, roomID, "OnMessage", &protocol.ChatMessage{
		SenderId: uid,
		Content:  msg.Content,
	})
	r.LogEvent("Msg from %s: %s", uid, msg.Content)
}

// Leave Handler
func (r *Room) Leave(ctx context.Context, req *protocol.LeaveRequest) (*protocol.LeaveResponse, error) {
	s := r.app.GetSessionFromCtx(ctx)
	uid := s.UID()

	val := s.Get("roomID")
	if val == nil {
		return &protocol.LeaveResponse{
			Code:    200,
			Message: "Not in any room",
		}, nil
	}
	roomID, ok := val.(string)
	if !ok || roomID == "" {
		// Even if roomID is not set in session, we should check if player is in global map
		// and clean up if necessary to fix state inconsistencies
		r.mu.Lock()
		_, exists := r.players[uid]
		r.mu.Unlock()
		if exists {
			// Find which room they are in?
			// This is expensive, but for correctness:
			r.mu.RLock()
			var realRoomID string
			for rid, room := range r.rooms {
				if room.HasPlayer(uid) {
					realRoomID = rid
					break
				}
			}
			r.mu.RUnlock()

			if realRoomID != "" {
				r.onPlayerDisconnect(uid, realRoomID)
			} else {
				// Just clean up global map
				r.mu.Lock()
				delete(r.players, uid)
				r.mu.Unlock()
			}
		}
		return &protocol.LeaveResponse{
			Code:    200,
			Message: "Left room successfully",
		}, nil
	}

	r.onPlayerDisconnect(uid, roomID)
	s.Set("roomID", "")

	return &protocol.LeaveResponse{
		Code:    200,
		Message: "Left room successfully",
	}, nil
}
