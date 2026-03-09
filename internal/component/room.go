package component

import (
	"context"
	"fmt"
	"sync"

	"3dtest-server/internal/config"
	"3dtest-server/internal/models"
	"3dtest-server/protocol"

	"github.com/google/uuid"
	"github.com/topfreegames/pitaya/v2"
	"github.com/topfreegames/pitaya/v2/component"
	"github.com/topfreegames/pitaya/v2/constants"
	"github.com/topfreegames/pitaya/v2/logger"
)

type GameRoom struct {
	ID         string
	Name       string
	MaxPlayers int
	Players    map[string]*models.Player
}

type Room struct {
	component.Base
	app     pitaya.Pitaya
	rooms   map[string]*GameRoom
	players map[string]*models.Player // uid -> Player
	mu      sync.RWMutex
}

func NewRoom(app pitaya.Pitaya) *Room {
	return &Room{
		app:     app,
		rooms:   make(map[string]*GameRoom),
		players: make(map[string]*models.Player),
	}
}

func (r *Room) Init() {
	// Create a default room for quick testing
	r.createRoom("Lobby", 100)
}

func (r *Room) createRoom(name string, maxPlayers int) *GameRoom {
	id := uuid.New().String()
	// Use simpler ID for Lobby if needed, but UUID is safer
	if name == "Lobby" {
		id = "lobby"
	}

	room := &GameRoom{
		ID:         id,
		Name:       name,
		MaxPlayers: maxPlayers,
		Players:    make(map[string]*models.Player),
	}

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
		return nil, fmt.Errorf("room name cannot be empty")
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
			Count: int32(len(room.Players)),
			Max:   int32(room.MaxPlayers),
		})
	}

	return res, nil
}

// Join Room Handler
func (r *Room) Join(ctx context.Context, req *protocol.JoinRequest) (*protocol.JoinResponse, error) {
	s := r.app.GetSessionFromCtx(ctx)
	uid := fmt.Sprintf("%d", s.ID())

	// Bind session if not already bound
	if s.UID() == "" {
		if err := s.Bind(ctx, uid); err != nil && err != constants.ErrSessionAlreadyBound {
			return nil, err
		}
	}

	r.mu.Lock()
	// Use a defer for unlock, but be careful with async operations or long holding
	// Here we hold lock for room check and player addition.
	// GroupAddMember is potentially slow (network/redis), so we should release lock if possible.
	// But releasing lock might cause race conditions on room capacity check.
	// For standalone in-memory, it's fast. For cluster, GroupAddMember is remote.
	// Since we are standalone, we can keep lock.
	defer r.mu.Unlock()

	roomID := req.RoomId
	if roomID == "" {
		roomID = "lobby" // Default to lobby
	}

	room, exists := r.rooms[roomID]
	if !exists {
		return nil, fmt.Errorf("room not found: %s", roomID)
	}

	if len(room.Players) >= room.MaxPlayers {
		return nil, fmt.Errorf("room is full")
	}

	// Create Player Model
	player := models.NewPlayer(uid, req.Name)
	room.Players[uid] = player
	r.players[uid] = player

	// Add to Pitaya Group
	// Note: We are holding the lock here. If GroupAddMember blocks, we block all room ops.
	// Ideally, we should reserve the slot, release lock, add to group, then commit.
	// But for simplicity in this demo, we hold lock.
	if err := r.app.GroupAddMember(ctx, roomID, uid); err != nil {
		logger.Log.Errorf("Failed to add member to group: %v", err)
		// Rollback
		delete(room.Players, uid)
		delete(r.players, uid)
		return nil, fmt.Errorf("failed to join room group")
	}

	// Save RoomID in session
	s.Set("roomID", roomID)

	// Notify others in room
	// Using GroupBroadcast to send PlayerJoinPush
	// This is async in Pitaya usually (pushes to channel), so it's fast.
	r.app.GroupBroadcast(ctx, config.Conf.Server.Type, roomID, "OnPlayerJoin", &protocol.PlayerJoinPush{
		Id:   player.ID,
		Name: player.Name,
	})

	// Handle Session Close
	s.OnClose(func() {
		r.onPlayerDisconnect(uid, roomID)
	})

	logger.Log.Infof("Player %s joined room %s", uid, roomID)

	return &protocol.JoinResponse{
		Code:    200,
		Message: "Joined successfully",
		RoomId:  roomID,
	}, nil
}

func (r *Room) onPlayerDisconnect(uid, roomID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Remove from global map
	delete(r.players, uid)

	// Remove from room
	if room, exists := r.rooms[roomID]; exists {
		delete(room.Players, uid)

		// Notify others
		ctx := context.Background()
		r.app.GroupRemoveMember(ctx, roomID, uid)
		r.app.GroupBroadcast(ctx, config.Conf.Server.Type, roomID, "OnPlayerLeave", &protocol.PlayerLeavePush{
			Id: uid,
		})

		logger.Log.Infof("Player %s left room %s", uid, roomID)
	}
}

// Move Handler (Notify)
func (r *Room) Move(ctx context.Context, req *protocol.MoveRequest) {
	s := r.app.GetSessionFromCtx(ctx)
	uid := s.UID()
	if uid == "" {
		return
	}

	// Optimized: Only check session for RoomID, no iteration fallback
	val := s.Get("roomID")
	if val == nil {
		return
	}
	roomID, ok := val.(string)
	if !ok || roomID == "" {
		return
	}

	// Lock only to update player state in memory
	r.mu.Lock()
	player, ok := r.players[uid]
	if ok {
		player.Position = req.Position
		player.Rotation = req.Rotation
	}
	r.mu.Unlock()

	if !ok {
		return
	}

	// Broadcast to others
	// Optimization: Don't broadcast to self? GroupBroadcast sends to all.
	// Clients usually ignore their own ID or predict movement.
	err := r.app.GroupBroadcast(ctx, config.Conf.Server.Type, roomID, "OnPlayerMove", &protocol.PlayerMovePush{
		Id:       uid,
		Position: req.Position,
		Rotation: req.Rotation,
	})
	if err != nil {
		logger.Log.Errorf("Move broadcast failed: %v", err)
	}
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

	// Broadcast
	r.app.GroupBroadcast(ctx, config.Conf.Server.Type, roomID, "OnMessage", &protocol.ChatMessage{
		SenderId: uid,
		Content:  msg.Content,
	})
}

// Leave Handler
func (r *Room) Leave(ctx context.Context, req *protocol.LeaveRequest) {
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

	r.onPlayerDisconnect(uid, roomID)
	// Unbind session roomID?
	s.Set("roomID", "")
}
