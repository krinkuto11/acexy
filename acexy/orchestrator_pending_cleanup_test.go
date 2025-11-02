package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestPendingStreamCleanup verifies that pending streams are properly released on FetchStream failure
func TestPendingStreamCleanup(t *testing.T) {
	// Create a mock orchestrator that tracks pending streams
	pendingCount := 0
	mockOrch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engines":
			engines := []engineState{
				{
					ContainerID:     "test-engine-1",
					ContainerName:   "test-1",
					Host:            "localhost",
					Port:            19000,
					HealthStatus:    "healthy",
					LastHealthCheck: time.Now(),
					Streams:         []string{},
				},
			}
			json.NewEncoder(w).Encode(engines)

		case "/streams":
			// Return empty streams list
			json.NewEncoder(w).Encode([]streamState{})

		case "/events/stream_started":
			// Track that a pending stream was allocated
			pendingCount++
			w.WriteHeader(http.StatusOK)

		case "/events/stream_ended":
			// Track that a pending stream was released
			if pendingCount > 0 {
				pendingCount--
			}
			w.WriteHeader(http.StatusOK)

		case "/orchestrator/status":
			status := orchestratorStatus{
				Status: "healthy",
				VPN:    struct{ Connected bool `json:"connected"` }{Connected: true},
				Provisioning: struct {
					CanProvision         bool            `json:"can_provision"`
					BlockedReason        string          `json:"blocked_reason"`
					BlockedReasonDetails *ProvisionError `json:"blocked_reason_details"`
				}{
					CanProvision:  true,
					BlockedReason: "",
				},
			}
			json.NewEncoder(w).Encode(status)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockOrch.Close()

	// Create orchestrator client
	client := newOrchClient(mockOrch.URL)
	client.SetMaxStreamsPerEngine(2)

	// Test 1: Verify pending stream is incremented when selecting engine
	host, port, containerID, err := client.SelectBestEngine()
	if err != nil {
		t.Fatalf("Expected successful engine selection, got error: %v", err)
	}
	if host != "localhost" || port != 19000 {
		t.Errorf("Expected localhost:19000, got %s:%d", host, port)
	}

	// Verify pending stream was incremented
	client.pendingStreamsMu.Lock()
	if client.pendingStreams[containerID] != 1 {
		t.Errorf("Expected 1 pending stream, got %d", client.pendingStreams[containerID])
	}
	client.pendingStreamsMu.Unlock()

	// Test 2: Simulate FetchStream failure and verify pending stream is released
	client.ReleasePendingStream(containerID)

	client.pendingStreamsMu.Lock()
	if client.pendingStreams[containerID] != 0 {
		t.Errorf("Expected 0 pending streams after release, got %d", client.pendingStreams[containerID])
	}
	client.pendingStreamsMu.Unlock()

	t.Log("Pending stream cleanup working correctly for FetchStream failure")
}

// TestStaleStreamCleanup verifies that stale pending streams are cleaned up periodically
func TestStaleStreamCleanup(t *testing.T) {
	client := newOrchClient("http://dummy")

	// Simulate some stale pending streams
	client.pendingStreamsMu.Lock()
	client.pendingStreams["engine-1"] = 2
	client.pendingStreams["engine-2"] = 1
	client.pendingStreamsMu.Unlock()

	// Verify they exist
	client.pendingStreamsMu.Lock()
	initialCount := len(client.pendingStreams)
	client.pendingStreamsMu.Unlock()

	if initialCount != 2 {
		t.Errorf("Expected 2 engines with pending streams, got %d", initialCount)
	}

	// Run cleanup
	client.cleanupStaleData()

	// Verify they are cleared
	client.pendingStreamsMu.Lock()
	finalCount := len(client.pendingStreams)
	client.pendingStreamsMu.Unlock()

	if finalCount != 0 {
		t.Errorf("Expected 0 pending streams after cleanup, got %d", finalCount)
	}

	t.Log("Stale stream cleanup working correctly")
}
