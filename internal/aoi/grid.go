package aoi

import (
	"errors"
	"sync"

	"game-server/protocol"
)

// GridManager implements the AOI Manager interface using a grid-based approach
type GridManager struct {
	minX, maxX float32
	minY, maxY float32
	gridSize   float32
	gridCountX int
	gridCountY int
	grids      map[int]map[string]bool // gridID -> set of entity IDs
	entityGrid map[string]int          // entityID -> gridID
	entityPos  map[string]*protocol.Vector3
	mu         sync.RWMutex
}

// NewGridManager creates a new grid-based AOI manager
func NewGridManager(minX, maxX, minY, maxY, gridSize float32) *GridManager {
	gridCountX := int((maxX - minX) / gridSize)
	if (maxX-minX)/gridSize > float32(gridCountX) {
		gridCountX++
	}
	gridCountY := int((maxY - minY) / gridSize)
	if (maxY-minY)/gridSize > float32(gridCountY) {
		gridCountY++
	}

	return &GridManager{
		minX:       minX,
		maxX:       maxX,
		minY:       minY,
		maxY:       maxY,
		gridSize:   gridSize,
		gridCountX: gridCountX,
		gridCountY: gridCountY,
		grids:      make(map[int]map[string]bool),
		entityGrid: make(map[string]int),
		entityPos:  make(map[string]*protocol.Vector3),
	}
}

// getGridID calculates the grid ID for a given position
// We use X and Z for 3D game ground plane (Unity uses Y for height)
func (g *GridManager) getGridID(pos *protocol.Vector3) int {
	x := pos.X
	z := pos.Z // Using Z as the second dimension for grid

	if x < g.minX {
		x = g.minX
	} else if x > g.maxX {
		x = g.maxX
	}

	if z < g.minY {
		z = g.minY
	} else if z > g.maxY {
		z = g.maxY
	}

	idxX := int((x - g.minX) / g.gridSize)
	idxY := int((z - g.minY) / g.gridSize)

	return idxY*g.gridCountX + idxX
}

// getSurroundingGrids returns the IDs of the grid and its neighbors
func (g *GridManager) getSurroundingGrids(gridID int) []int {
	grids := make([]int, 0, 9)
	grids = append(grids, gridID)

	idxX := gridID % g.gridCountX
	idxY := gridID / g.gridCountX

	// Check 8 neighbors
	for y := -1; y <= 1; y++ {
		for x := -1; x <= 1; x++ {
			if x == 0 && y == 0 {
				continue
			}

			nX := idxX + x
			nY := idxY + y

			if nX >= 0 && nX < g.gridCountX && nY >= 0 && nY < g.gridCountY {
				grids = append(grids, nY*g.gridCountX+nX)
			}
		}
	}

	return grids
}

func (g *GridManager) Enter(id string, pos *protocol.Vector3) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.entityGrid[id]; exists {
		return errors.New("entity already exists in AOI")
	}

	gridID := g.getGridID(pos)

	if _, exists := g.grids[gridID]; !exists {
		g.grids[gridID] = make(map[string]bool)
	}

	g.grids[gridID][id] = true
	g.entityGrid[id] = gridID
	// Store a copy of position
	g.entityPos[id] = &protocol.Vector3{X: pos.X, Y: pos.Y, Z: pos.Z}

	return nil
}

func (g *GridManager) Leave(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	gridID, exists := g.entityGrid[id]
	if !exists {
		return errors.New("entity not found in AOI")
	}

	delete(g.grids[gridID], id)
	if len(g.grids[gridID]) == 0 {
		delete(g.grids, gridID)
	}
	delete(g.entityGrid, id)
	delete(g.entityPos, id)

	return nil
}

func (g *GridManager) Move(id string, newPos *protocol.Vector3) ([]string, []string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	oldGridID, exists := g.entityGrid[id]
	if !exists {
		return nil, nil, errors.New("entity not found in AOI")
	}

	newGridID := g.getGridID(newPos)

	// Update position storage
	g.entityPos[id].X = newPos.X
	g.entityPos[id].Y = newPos.Y
	g.entityPos[id].Z = newPos.Z

	// If grid hasn't changed, neighbors haven't changed in a significant way for AOI events
	// (though position update still needs to be broadcast to current neighbors)
	if oldGridID == newGridID {
		return nil, nil, nil
	}

	// Move entity to new grid
	delete(g.grids[oldGridID], id)
	if len(g.grids[oldGridID]) == 0 {
		delete(g.grids, oldGridID)
	}

	if _, exists := g.grids[newGridID]; !exists {
		g.grids[newGridID] = make(map[string]bool)
	}
	g.grids[newGridID][id] = true
	g.entityGrid[id] = newGridID

	// Calculate AOI changes
	oldSurrounding := g.getSurroundingGrids(oldGridID)
	newSurrounding := g.getSurroundingGrids(newGridID)

	oldSet := make(map[int]bool)
	for _, gid := range oldSurrounding {
		oldSet[gid] = true
	}

	newSet := make(map[int]bool)
	for _, gid := range newSurrounding {
		newSet[gid] = true
	}

	// Entities that leave view (in old set but not new set)
	leaveIDs := make([]string, 0)
	for _, gid := range oldSurrounding {
		if !newSet[gid] {
			for eid := range g.grids[gid] {
				if eid != id {
					leaveIDs = append(leaveIDs, eid)
				}
			}
		}
	}

	// Entities that enter view (in new set but not old set)
	enterIDs := make([]string, 0)
	for _, gid := range newSurrounding {
		if !oldSet[gid] {
			for eid := range g.grids[gid] {
				if eid != id {
					enterIDs = append(enterIDs, eid)
				}
			}
		}
	}

	return leaveIDs, enterIDs, nil
}

func (g *GridManager) GetNeighbors(id string) ([]string, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	gridID, exists := g.entityGrid[id]
	if !exists {
		return nil, errors.New("entity not found in AOI")
	}

	surrounding := g.getSurroundingGrids(gridID)
	neighbors := make([]string, 0)

	for _, gid := range surrounding {
		for eid := range g.grids[gid] {
			if eid != id {
				neighbors = append(neighbors, eid)
			}
		}
	}

	return neighbors, nil
}

// GetNeighborsByPos returns neighbors for a position without requiring an entity ID
// Useful for initial join when entity is not yet in AOI
func (g *GridManager) GetNeighborsByPos(pos *protocol.Vector3) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	gridID := g.getGridID(pos)
	surrounding := g.getSurroundingGrids(gridID)
	neighbors := make([]string, 0)

	for _, gid := range surrounding {
		for eid := range g.grids[gid] {
			neighbors = append(neighbors, eid)
		}
	}

	return neighbors
}
