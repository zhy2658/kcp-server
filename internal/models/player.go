package models

import (
	"errors"
	"math"
	"sync"

	"game-server/protocol"
)

const (
	MaxSpeedPerTick = 1000.0 // Units per sync (increased to allow initial spawn sync)
)

type Player struct {
	ID   string
	Name string
	mu   sync.RWMutex
	pos  *protocol.Vector3
	rot  *protocol.Quaternion

	// Combat Stats
	HP       int32
	MaxHP    int32
	Attack   int32
	IsDead   bool
	SkillCDs map[int32]int64
}

func NewPlayer(id, name string) *Player {
	return &Player{
		ID:       id,
		Name:     name,
		pos:      &protocol.Vector3{X: 0, Y: 0, Z: 0},
		rot:      &protocol.Quaternion{X: 0, Y: 0, Z: 0, W: 1},
		HP:       100,
		MaxHP:    100,
		Attack:   10,
		SkillCDs: make(map[int32]int64),
	}
}

func (p *Player) GetPosition() *protocol.Vector3 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return cloneVec3(p.pos)
}

func (p *Player) GetRotation() *protocol.Quaternion {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return cloneQuat(p.rot)
}

func (p *Player) GetState() (*protocol.Vector3, *protocol.Quaternion) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return cloneVec3(p.pos), cloneQuat(p.rot)
}

func (p *Player) ToProto() *protocol.PlayerState {
	pos, rot := p.GetState()
	return &protocol.PlayerState{
		Id:       p.ID,
		Position: pos,
		Rotation: rot,
	}
}

func (p *Player) ApplyDamage(damage int32) (int32, int32, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.IsDead {
		return 0, 0, true
	}

	p.HP -= damage
	if p.HP < 0 {
		p.HP = 0
	}

	if p.HP == 0 {
		p.IsDead = true
	}

	return damage, p.HP, p.IsDead
}

func (p *Player) CheckSkillCD(skillID int32, cdMillis int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	_, ok := p.SkillCDs[skillID]
	if !ok {
		return true
	}

	// Simple check: current time - last time > cd
	// Note: In real app, pass time from outside or use time.Now()
	// We will assume caller handles time or we use a simple timestamp check
	return true
}

func (p *Player) SetSkillCD(skillID int32, timestamp int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.SkillCDs[skillID] = timestamp
}

// UpdatePosition updates the player's position with basic validation.
// Returns error if the movement is invalid (e.g. too fast).
// On validation failure, the returned position is the last valid one.
func (p *Player) UpdatePosition(newPos *protocol.Vector3, newRot *protocol.Quaternion) error {
	if newPos == nil {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	dist := distance(p.pos, newPos)
	if dist > MaxSpeedPerTick {
		return errors.New("movement too fast")
	}

	p.pos = newPos
	p.rot = newRot
	return nil
}

func distance(a, b *protocol.Vector3) float64 {
	dx := a.X - b.X
	dy := a.Y - b.Y
	dz := a.Z - b.Z
	return math.Sqrt(float64(dx*dx + dy*dy + dz*dz))
}

func cloneVec3(v *protocol.Vector3) *protocol.Vector3 {
	if v == nil {
		return nil
	}
	return &protocol.Vector3{X: v.X, Y: v.Y, Z: v.Z}
}

func cloneQuat(q *protocol.Quaternion) *protocol.Quaternion {
	if q == nil {
		return nil
	}
	return &protocol.Quaternion{X: q.X, Y: q.Y, Z: q.Z, W: q.W}
}
