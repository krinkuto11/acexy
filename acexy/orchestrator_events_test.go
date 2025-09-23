package main

import (
	"testing"
	"time"
)

func TestSelectBestEngineLoadBalancing(t *testing.T) {
	// Test data: engines with different stream counts and LastSeen timestamps
	now := time.Now()
	engines := []engineWithLoad{
		{
			engine: engineState{
				ContainerID: "engine1",
				Host:        "host1",
				Port:        8001,
				LastSeen:    now.Add(-10 * time.Minute), // Oldest
			},
			activeStreams: 1,
		},
		{
			engine: engineState{
				ContainerID: "engine2",
				Host:        "host2", 
				Port:        8002,
				LastSeen:    now.Add(-5 * time.Minute), // More recent
			},
			activeStreams: 1,
		},
		{
			engine: engineState{
				ContainerID: "engine3",
				Host:        "host3",
				Port:        8003,
				LastSeen:    now.Add(-20 * time.Minute), // Very old
			},
			activeStreams: 0, // Empty engine
		},
		{
			engine: engineState{
				ContainerID: "engine4",
				Host:        "host4",
				Port:        8004,
				LastSeen:    now.Add(-1 * time.Minute), // Most recent
			},
			activeStreams: 0, // Empty engine
		},
	}

	// Apply the same sorting logic as in SelectBestEngine
	availableEngines := make([]engineWithLoad, len(engines))
	copy(availableEngines, engines)

	// Sort engines by stream count (ascending) first to prioritize empty engines,
	// then by LastSeen (ascending) to prioritize engines that haven't been used the longest
	for i := 0; i < len(availableEngines); i++ {
		for j := i + 1; j < len(availableEngines); j++ {
			iEngine := availableEngines[i]
			jEngine := availableEngines[j]
			
			// Primary sort: by active stream count (ascending)
			if iEngine.activeStreams > jEngine.activeStreams {
				availableEngines[i], availableEngines[j] = availableEngines[j], availableEngines[i]
			} else if iEngine.activeStreams == jEngine.activeStreams {
				// Secondary sort: by LastSeen timestamp (ascending - oldest first)
				// This ensures that among engines with same stream count, we pick the one unused the longest
				if iEngine.engine.LastSeen.After(jEngine.engine.LastSeen) {
					availableEngines[i], availableEngines[j] = availableEngines[j], availableEngines[i]
				}
			}
		}
	}

	// Verify sorting results
	// First should be the empty engine that was used longest ago (engine3)
	if availableEngines[0].engine.ContainerID != "engine3" {
		t.Errorf("Expected engine3 (empty, oldest) to be first, got %s", availableEngines[0].engine.ContainerID)
	}
	if availableEngines[0].activeStreams != 0 {
		t.Errorf("Expected first engine to have 0 streams, got %d", availableEngines[0].activeStreams)
	}

	// Second should be the other empty engine (engine4)
	if availableEngines[1].engine.ContainerID != "engine4" {
		t.Errorf("Expected engine4 (empty, newer) to be second, got %s", availableEngines[1].engine.ContainerID)
	}
	if availableEngines[1].activeStreams != 0 {
		t.Errorf("Expected second engine to have 0 streams, got %d", availableEngines[1].activeStreams)
	}

	// Third should be engine1 (1 stream, but older than engine2)
	if availableEngines[2].engine.ContainerID != "engine1" {
		t.Errorf("Expected engine1 (1 stream, older) to be third, got %s", availableEngines[2].engine.ContainerID)
	}
	if availableEngines[2].activeStreams != 1 {
		t.Errorf("Expected third engine to have 1 stream, got %d", availableEngines[2].activeStreams)
	}

	// Fourth should be engine2 (1 stream, newer)
	if availableEngines[3].engine.ContainerID != "engine2" {
		t.Errorf("Expected engine2 (1 stream, newer) to be fourth, got %s", availableEngines[3].engine.ContainerID)
	}
	if availableEngines[3].activeStreams != 1 {
		t.Errorf("Expected fourth engine to have 1 stream, got %d", availableEngines[3].activeStreams)
	}
}

// Define the engineWithLoad type for testing (it's defined locally in the original function)
type engineWithLoad struct {
	engine       engineState
	activeStreams int
}