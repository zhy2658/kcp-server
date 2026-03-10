package models

import (
	"errors"
	"math"

	"game-server/protocol"
)

const (
	MaxSpeedPerTick = 10.0 // Units per sync (adjust based on game logic)
)

type Player struct {
	ID       string
	Name     string
	Position *protocol.Vector3
	Rotation *protocol.Quaternion
}

func NewPlayer(id, name string) *Player {
	return &Player{
		ID:       id,
		Name:     name,
		Position: &protocol.Vector3{X: 0, Y: 0, Z: 0},
		Rotation: &protocol.Quaternion{X: 0, Y: 0, Z: 0, W: 1},
	}
}

func (p *Player) ToProto() *protocol.PlayerState {
	return &protocol.PlayerState{
		Id:       p.ID,
		Position: p.Position,
		Rotation: p.Rotation,
	}
}

// UpdatePosition updates the player's position with basic validation.
// Returns error if the movement is invalid (e.g. too fast).
func (p *Player) UpdatePosition(newPos *protocol.Vector3, newRot *protocol.Quaternion) error {
	if newPos == nil {
		return nil
	}

	dist := distance(p.Position, newPos)
	if dist > MaxSpeedPerTick {
		return errors.New("movement too fast")
	}

	p.Position = newPos
	p.Rotation = newRot
	return nil
}

func distance(a, b *protocol.Vector3) float64 {
	dx := a.X - b.X
	dy := a.Y - b.Y
	dz := a.Z - b.Z
	return math.Sqrt(float64(dx*dx + dy*dy + dz*dz))
}
