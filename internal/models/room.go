package models

import (
	"errors"
	"sync"

	"game-server/internal/aoi"
)

type GameRoom struct {
	ID         string
	Name       string
	MaxPlayers int
	Players    map[string]*Player
	AOI        aoi.Manager
	mu         sync.RWMutex
}

func NewGameRoom(id, name string, maxPlayers int) *GameRoom {
	return &GameRoom{
		ID:         id,
		Name:       name,
		MaxPlayers: maxPlayers,
		Players:    make(map[string]*Player),
		AOI:        aoi.NewGridManager(-100, 100, -100, 100, 20), // Default 200x200 world, 20 unit grid
	}
}

// AddPlayer adds a player to the room if not full.
// Returns error if room is full or player already exists.
func (r *GameRoom) AddPlayer(p *Player) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.Players) >= r.MaxPlayers {
		return errors.New("room is full")
	}

	if _, exists := r.Players[p.ID]; exists {
		return errors.New("player already in room")
	}

	// Add to AOI
	if err := r.AOI.Enter(p.ID, p.Position); err != nil {
		return err
	}

	r.Players[p.ID] = p
	return nil
}

// RemovePlayer removes a player from the room.
func (r *GameRoom) RemovePlayer(uid string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Remove from AOI first
	r.AOI.Leave(uid)

	delete(r.Players, uid)
}

// GetPlayer returns a player by UID.
func (r *GameRoom) GetPlayer(uid string) *Player {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.Players[uid]
}

// PlayerCount returns the current number of players.
func (r *GameRoom) PlayerCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.Players)
}

// HasPlayer checks if a player is in the room.
func (r *GameRoom) HasPlayer(uid string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.Players[uid]
	return exists
}

// GetPlayers returns a safe copy of the player list
func (r *GameRoom) GetPlayers() []*Player {
	r.mu.RLock()
	defer r.mu.RUnlock()
	players := make([]*Player, 0, len(r.Players))
	for _, p := range r.Players {
		players = append(players, p)
	}
	return players
}
