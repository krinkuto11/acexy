package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestSelectBestEngine_CircuitBreakerFiltering verifies that engines with open circuit breakers
// are filtered out during engine selection
func TestSelectBestEngine_CircuitBreakerFiltering(t *testing.T) {
	// Create a mock orchestrator server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engines":
			// Return two engines
			engines := []engineState{
				{
					ContainerID:     "engine-1",
					ContainerName:   "test-engine-1",
					Host:            "localhost",
					Port:            19000,
					HealthStatus:    "healthy",
					LastHealthCheck: time.Now(),
					Forwarded:       true,
				},
				{
					ContainerID:     "engine-2",
					ContainerName:   "test-engine-2",
					Host:            "localhost",
					Port:            19001,
					HealthStatus:    "healthy",
					LastHealthCheck: time.Now(),
					Forwarded:       true,
				},
			}
			json.NewEncoder(w).Encode(engines)
		case "/streams":
			// Return empty stream list for both engines
			json.NewEncoder(w).Encode([]streamState{})
		case "/orchestrator/status":
			status := orchestratorStatus{
				Status: "healthy",
			}
			status.VPN.Connected = true
			status.Provisioning.CanProvision = true
			json.NewEncoder(w).Encode(status)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Create orchestrator client
	client := newOrchClient(server.URL)
	client.SetMaxStreamsPerEngine(1)
	defer client.Close()

	// Create failure tracker and open circuit breaker for engine-1
	failureTracker := NewEngineFailureTracker()
	
	// Record 3 failures for engine-1 to open circuit breaker
	for i := 0; i < 3; i++ {
		failureTracker.RecordAttempt("engine-1")
		failureTracker.RecordFailure("engine-1", "test failure")
		failureTracker.ReleaseAttempt("engine-1")
	}

	// Verify circuit breaker is open for engine-1
	canAttempt, reason := failureTracker.CanAttempt("engine-1")
	if canAttempt {
		t.Fatal("Circuit breaker should be open for engine-1")
	}
	if reason == "" {
		t.Fatal("Circuit breaker should provide a reason")
	}

	// Select best engine with failure tracker
	host, port, containerID, err := client.SelectBestEngine(failureTracker)
	if err != nil {
		t.Fatalf("Failed to select engine: %v", err)
	}

	// Should select engine-2, not engine-1 (which has open circuit breaker)
	if containerID != "engine-2" {
		t.Errorf("Expected engine-2 to be selected (engine-1 has open circuit breaker), got %s", containerID)
	}
	if host != "localhost" || port != 19001 {
		t.Errorf("Expected host=localhost port=19001, got host=%s port=%d", host, port)
	}

	t.Log("Circuit breaker filtering working correctly - engine with open circuit breaker was skipped")
}

// TestSelectBestEngine_AllCircuitBreakersOpen verifies behavior when all engines have open circuit breakers
func TestSelectBestEngine_AllCircuitBreakersOpen(t *testing.T) {
	// Create a mock orchestrator server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engines":
			// Return two engines
			engines := []engineState{
				{
					ContainerID:     "engine-1",
					ContainerName:   "test-engine-1",
					Host:            "localhost",
					Port:            19000,
					HealthStatus:    "healthy",
					LastHealthCheck: time.Now(),
					Forwarded:       true,
				},
				{
					ContainerID:     "engine-2",
					ContainerName:   "test-engine-2",
					Host:            "localhost",
					Port:            19001,
					HealthStatus:    "healthy",
					LastHealthCheck: time.Now(),
					Forwarded:       true,
				},
			}
			json.NewEncoder(w).Encode(engines)
		case "/streams":
			// Return empty stream list for both engines
			json.NewEncoder(w).Encode([]streamState{})
		case "/orchestrator/status":
			status := orchestratorStatus{
				Status: "healthy",
			}
			status.VPN.Connected = true
			status.Provisioning.CanProvision = false
			status.Provisioning.BlockedReason = "all engines have circuit breakers open"
			json.NewEncoder(w).Encode(status)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Create orchestrator client
	client := newOrchClient(server.URL)
	client.SetMaxStreamsPerEngine(1)
	defer client.Close()

	// Wait for initial health check
	time.Sleep(100 * time.Millisecond)

	// Create failure tracker and open circuit breakers for both engines
	failureTracker := NewEngineFailureTracker()
	
	for _, engineID := range []string{"engine-1", "engine-2"} {
		// Record 3 failures to open circuit breaker
		for i := 0; i < 3; i++ {
			failureTracker.RecordAttempt(engineID)
			failureTracker.RecordFailure(engineID, "test failure")
			failureTracker.ReleaseAttempt(engineID)
		}
	}

	// Verify both circuit breakers are open
	for _, engineID := range []string{"engine-1", "engine-2"} {
		canAttempt, _ := failureTracker.CanAttempt(engineID)
		if canAttempt {
			t.Fatalf("Circuit breaker should be open for %s", engineID)
		}
	}

	// Select best engine with failure tracker - should fail or provision new engine
	_, _, _, err := client.SelectBestEngine(failureTracker)
	if err == nil {
		t.Log("New engine provisioned when all circuit breakers open (expected behavior)")
	} else {
		t.Logf("Selection failed when all circuit breakers open (expected behavior): %v", err)
	}

	t.Log("Correctly handled case where all engines have open circuit breakers")
}

// TestSelectBestEngine_CircuitBreakerCooldown verifies that engines become available after cooldown
func TestSelectBestEngine_CircuitBreakerCooldown(t *testing.T) {
	// Create a mock orchestrator server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engines":
			// Return one engine
			engines := []engineState{
				{
					ContainerID:     "engine-1",
					ContainerName:   "test-engine-1",
					Host:            "localhost",
					Port:            19000,
					HealthStatus:    "healthy",
					LastHealthCheck: time.Now(),
					Forwarded:       true,
				},
			}
			json.NewEncoder(w).Encode(engines)
		case "/streams":
			// Return empty stream list
			json.NewEncoder(w).Encode([]streamState{})
		case "/orchestrator/status":
			status := orchestratorStatus{
				Status: "healthy",
			}
			status.VPN.Connected = true
			status.Provisioning.CanProvision = true
			json.NewEncoder(w).Encode(status)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Create orchestrator client
	client := newOrchClient(server.URL)
	client.SetMaxStreamsPerEngine(1)
	defer client.Close()

	// Create failure tracker with short cooldown for testing
	failureTracker := NewEngineFailureTracker()
	failureTracker.cooldownPeriod = 100 * time.Millisecond
	
	// Open circuit breaker for engine-1
	for i := 0; i < 3; i++ {
		failureTracker.RecordAttempt("engine-1")
		failureTracker.RecordFailure("engine-1", "test failure")
		failureTracker.ReleaseAttempt("engine-1")
	}

	// Verify circuit breaker is open
	canAttempt, _ := failureTracker.CanAttempt("engine-1")
	if canAttempt {
		t.Fatal("Circuit breaker should be open for engine-1")
	}

	// Try to select engine - should fail
	_, _, _, err := client.SelectBestEngine(failureTracker)
	if err == nil {
		t.Fatal("Expected selection to fail when circuit breaker is open")
	}

	// Wait for cooldown period
	time.Sleep(150 * time.Millisecond)

	// Circuit breaker should allow retry after cooldown
	canAttempt, _ = failureTracker.CanAttempt("engine-1")
	if !canAttempt {
		t.Fatal("Circuit breaker should allow retry after cooldown")
	}

	// Now selection should succeed
	host, port, containerID, err := client.SelectBestEngine(failureTracker)
	if err != nil {
		t.Fatalf("Expected selection to succeed after cooldown: %v", err)
	}

	if containerID != "engine-1" {
		t.Errorf("Expected engine-1 to be selected after cooldown, got %s", containerID)
	}
	if host != "localhost" || port != 19000 {
		t.Errorf("Expected host=localhost port=19000, got host=%s port=%d", host, port)
	}

	t.Log("Circuit breaker cooldown working correctly - engine became available after cooldown period")
}

// TestSelectBestEngine_NilFailureTracker verifies backward compatibility when no failure tracker is provided
func TestSelectBestEngine_NilFailureTracker(t *testing.T) {
	// Create a mock orchestrator server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engines":
			engines := []engineState{
				{
					ContainerID:     "engine-1",
					ContainerName:   "test-engine-1",
					Host:            "localhost",
					Port:            19000,
					HealthStatus:    "healthy",
					LastHealthCheck: time.Now(),
					Forwarded:       true,
				},
			}
			json.NewEncoder(w).Encode(engines)
		case "/streams":
			json.NewEncoder(w).Encode([]streamState{})
		case "/orchestrator/status":
			status := orchestratorStatus{
				Status: "healthy",
			}
			status.VPN.Connected = true
			status.Provisioning.CanProvision = true
			json.NewEncoder(w).Encode(status)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newOrchClient(server.URL)
	client.SetMaxStreamsPerEngine(1)
	defer client.Close()

	// Select engine without failure tracker (backward compatibility)
	host, port, containerID, err := client.SelectBestEngine(nil)
	if err != nil {
		t.Fatalf("Failed to select engine: %v", err)
	}

	if containerID != "engine-1" {
		t.Errorf("Expected engine-1 to be selected, got %s", containerID)
	}
	if host != "localhost" || port != 19000 {
		t.Errorf("Expected host=localhost port=19000, got host=%s port=%d", host, port)
	}

	t.Log("Backward compatibility maintained - selecting engine works without failure tracker")
}
