package aoi

import "3dtest-server/protocol"

// Manager defines the interface for AOI management
type Manager interface {
	// Enter adds an entity to the AOI system
	Enter(id string, pos *protocol.Vector3) error

	// Leave removes an entity from the AOI system
	Leave(id string) error

	// Move updates an entity's position and returns the list of entities that should be notified
	// Returns:
	// - oldNeighbors: entities that can no longer see the moving entity (leave AOI)
	// - newNeighbors: entities that can now see the moving entity (enter AOI)
	Move(id string, newPos *protocol.Vector3) ([]string, []string, error)

	// GetNeighbors returns all entities within the AOI of the given entity
	GetNeighbors(id string) ([]string, error)
}
