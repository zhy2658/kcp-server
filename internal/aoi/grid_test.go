package aoi

import (
	"sort"
	"testing"

	"3dtest-server/protocol"
)

func TestGridManager_EnterAndLeave(t *testing.T) {
	gm := NewGridManager(0, 100, 0, 100, 10)

	pos := &protocol.Vector3{X: 5, Y: 0, Z: 5}
	err := gm.Enter("p1", pos)
	if err != nil {
		t.Fatalf("Failed to enter AOI: %v", err)
	}

	// Check if p1 is in grid
	gridID := gm.getGridID(pos)
	if _, exists := gm.grids[gridID]["p1"]; !exists {
		t.Errorf("Entity p1 should be in grid %d", gridID)
	}

	// Test duplicate enter
	err = gm.Enter("p1", pos)
	if err == nil {
		t.Error("Expected error for duplicate enter, got nil")
	}

	// Test Leave
	err = gm.Leave("p1")
	if err != nil {
		t.Fatalf("Failed to leave AOI: %v", err)
	}

	if _, exists := gm.grids[gridID]["p1"]; exists {
		t.Error("Entity p1 should be removed from grid")
	}

	// Test leave non-existent
	err = gm.Leave("p1")
	if err == nil {
		t.Error("Expected error for leaving non-existent entity, got nil")
	}
}

func TestGridManager_GetNeighbors(t *testing.T) {
	gm := NewGridManager(0, 100, 0, 100, 10)

	// p1 at (5,5) - Grid 0
	gm.Enter("p1", &protocol.Vector3{X: 5, Y: 0, Z: 5})

	// p2 at (15,5) - Grid 1 (Neighbor of 0)
	gm.Enter("p2", &protocol.Vector3{X: 15, Y: 0, Z: 5})

	// p3 at (95,95) - Far away
	gm.Enter("p3", &protocol.Vector3{X: 95, Y: 0, Z: 95})

	neighbors, err := gm.GetNeighbors("p1")
	if err != nil {
		t.Fatalf("GetNeighbors failed: %v", err)
	}

	if len(neighbors) != 1 {
		t.Errorf("Expected 1 neighbor for p1, got %d", len(neighbors))
	}
	if len(neighbors) > 0 && neighbors[0] != "p2" {
		t.Errorf("Expected neighbor p2, got %s", neighbors[0])
	}

	// Check p3 neighbors (should be empty)
	neighbors, _ = gm.GetNeighbors("p3")
	if len(neighbors) != 0 {
		t.Errorf("Expected 0 neighbors for p3, got %d", len(neighbors))
	}
}

func TestGridManager_Move(t *testing.T) {
	gm := NewGridManager(0, 100, 0, 100, 10)

	// p1 at (5,5) - Grid (0,0) -> ID 0
	gm.Enter("p1", &protocol.Vector3{X: 5, Y: 0, Z: 5})

	// p2 at (15,5) - Grid (1,0) -> ID 1
	gm.Enter("p2", &protocol.Vector3{X: 15, Y: 0, Z: 5})

	// Move p1 to (25, 5) - Grid (2,0) -> ID 2
	// Neighbors of Grid 0: 0, 1, 10, 11 (assuming 10x10 grid)
	// Neighbors of Grid 2: 1, 2, 3, 11, 12, 13

	// When p1 moves from 0 to 2:
	// p2 is in Grid 1.
	// Grid 1 is neighbor of 0.
	// Grid 1 is neighbor of 2.
	// So p2 should NOT be in leaveIDs or enterIDs (it stays visible).

	leave, enter, err := gm.Move("p1", &protocol.Vector3{X: 25, Y: 0, Z: 5})
	if err != nil {
		t.Fatalf("Move failed: %v", err)
	}

	if len(leave) != 0 {
		t.Errorf("Expected 0 leave IDs, got %v", leave)
	}
	if len(enter) != 0 {
		t.Errorf("Expected 0 enter IDs, got %v", enter)
	}

	// Now move p1 far away to (95,95)
	leave, enter, err = gm.Move("p1", &protocol.Vector3{X: 95, Y: 0, Z: 95})

	// p2 (Grid 1) should be in leave IDs because Grid 1 is not neighbor of (9,9)
	foundP2 := false
	for _, id := range leave {
		if id == "p2" {
			foundP2 = true
			break
		}
	}
	if !foundP2 {
		t.Errorf("Expected p2 in leave IDs, got %v", leave)
	}
}

func TestGridManager_CrossBoundary(t *testing.T) {
	// 20x20 world, grid size 10 -> 2x2 grids
	// IDs: 0, 1
	//      2, 3
	gm := NewGridManager(0, 20, 0, 20, 10)

	// p1 at (5,5) -> Grid 0
	gm.Enter("p1", &protocol.Vector3{X: 5, Y: 0, Z: 5})

	// p2 at (15,15) -> Grid 3
	gm.Enter("p2", &protocol.Vector3{X: 15, Y: 0, Z: 15})

	// Grid 0 neighbors: 0, 1, 2, 3 (All grids are neighbors in 2x2)
	neighbors, _ := gm.GetNeighbors("p1")

	// Sort for deterministic check
	sort.Strings(neighbors)

	if len(neighbors) != 1 {
		t.Errorf("Expected 1 neighbor (p2), got %d: %v", len(neighbors), neighbors)
	}
}
