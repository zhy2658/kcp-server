package models

import (
	"3dtest-server/protocol"
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
