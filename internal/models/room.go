package models

import (
	"errors"
	"sync"
)

type GameRoom struct {
	ID         string
	Name       string
	MaxPlayers int
	Players    map[string]*Player
	mu         sync.RWMutex
}

func NewGameRoom(id, name string, maxPlayers int) *GameRoom {
	return &GameRoom{
		ID:         id,
		Name:       name,
		MaxPlayers: maxPlayers,
		Players:    make(map[string]*Player),
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

	r.Players[p.ID] = p
	return nil
}

// RemovePlayer removes a player from the room.
func (r *GameRoom) RemovePlayer(uid string) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
