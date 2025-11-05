package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestRecordEngineFailure tests that failures are properly recorded
func TestRecordEngineFailure(t *testing.T) {
	client := &orchClient{
		engineErrors: make(map[string]*engineErrorState),
	}

	containerID := "test-engine-1"

	// Record a single failure
	client.RecordEngineFailure(containerID)

	client.engineErrorsMu.RLock()
	state := client.engineErrors[containerID]
	client.engineErrorsMu.RUnlock()

	if state == nil {
		t.Fatal("Expected error state to be created")
	}

	if state.failCount != 1 {
		t.Errorf("Expected fail count 1, got %d", state.failCount)
	}

	if state.recovering {
		t.Error("Engine should not be recovering after just 1 failure")
	}
}

// TestRecordEngineFailure_Threshold tests that engine is marked recovering at threshold
func TestRecordEngineFailure_Threshold(t *testing.T) {
	client := &orchClient{
		engineErrors: make(map[string]*engineErrorState),
	}

	containerID := "test-engine-1"

	// Record failures up to threshold
	for i := 0; i < engineFailureThreshold; i++ {
		client.RecordEngineFailure(containerID)
	}

	client.engineErrorsMu.RLock()
	state := client.engineErrors[containerID]
	client.engineErrorsMu.RUnlock()

	if state == nil {
		t.Fatal("Expected error state to be created")
	}

	if state.failCount != engineFailureThreshold {
		t.Errorf("Expected fail count %d, got %d", engineFailureThreshold, state.failCount)
	}

	if !state.recovering {
		t.Error("Engine should be marked as recovering after reaching threshold")
	}

	if state.recoveryStart.IsZero() {
		t.Error("Recovery start time should be set")
	}
}

// TestIsEngineRecovering tests the recovery status check
func TestIsEngineRecovering(t *testing.T) {
	client := &orchClient{
		engineErrors: make(map[string]*engineErrorState),
	}

	containerID := "test-engine-1"

	// Initially, engine should not be recovering
	if client.IsEngineRecovering(containerID) {
		t.Error("Engine should not be recovering initially")
	}

	// Mark engine as recovering
	client.engineErrors[containerID] = &engineErrorState{
		failCount:     engineFailureThreshold,
		recovering:    true,
		recoveryStart: time.Now(),
	}

	// Should be recovering now
	if !client.IsEngineRecovering(containerID) {
		t.Error("Engine should be recovering")
	}
}

// TestIsEngineRecovering_Expired tests that recovery expires after the period
func TestIsEngineRecovering_Expired(t *testing.T) {
	client := &orchClient{
		engineErrors: make(map[string]*engineErrorState),
	}

	containerID := "test-engine-1"

	// Mark engine as recovering but with an expired recovery start time
	client.engineErrors[containerID] = &engineErrorState{
		failCount:     engineFailureThreshold,
		recovering:    true,
		recoveryStart: time.Now().Add(-engineRecoveryPeriod - time.Second), // 61 seconds ago
	}

	// Should not be recovering anymore (period expired)
	if client.IsEngineRecovering(containerID) {
		t.Error("Engine should not be recovering after period expired")
	}

	// State should be cleaned up
	client.engineErrorsMu.RLock()
	_, exists := client.engineErrors[containerID]
	client.engineErrorsMu.RUnlock()

	if exists {
		t.Error("Error state should be cleaned up after recovery period expires")
	}
}

// TestResetEngineErrors tests manual error state reset
func TestResetEngineErrors(t *testing.T) {
	client := &orchClient{
		engineErrors: make(map[string]*engineErrorState),
	}

	containerID := "test-engine-1"

	// Add some failures
	client.engineErrors[containerID] = &engineErrorState{
		failCount:   3,
		recovering:  false,
		lastFailure: time.Now(),
	}

	// Reset errors
	client.ResetEngineErrors(containerID)

	// State should be cleared
	client.engineErrorsMu.RLock()
	_, exists := client.engineErrors[containerID]
	client.engineErrorsMu.RUnlock()

	if exists {
		t.Error("Error state should be cleared after reset")
	}
}

// TestSelectBestEngine_SkipsRecovering tests that recovering engines are skipped
func TestSelectBestEngine_SkipsRecovering(t *testing.T) {
	now := time.Now()
	
	// Create mock server for orchestrator
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/engines" {
			// Return two engines
			engines := []engineState{
				{
					ContainerID:     "healthy-engine",
					Host:            "localhost",
					Port:            19000,
					HealthStatus:    "healthy",
					LastHealthCheck: now,
					LastStreamUsage: now.Add(-10 * time.Minute),
				},
				{
					ContainerID:     "recovering-engine",
					Host:            "localhost",
					Port:            19001,
					HealthStatus:    "healthy",
					LastHealthCheck: now,
					LastStreamUsage: now.Add(-5 * time.Minute), // More recently used, would normally be preferred
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(engines)
			return
		}
		if r.URL.Path == "/streams" {
			// Return no streams - both engines are empty
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]streamState{})
			return
		}
		t.Errorf("Unexpected request to %s", r.URL.Path)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := &orchClient{
		base:                server.URL,
		maxStreamsPerEngine: 1,
		hc:                  &http.Client{Timeout: 3 * time.Second},
		ctx:                 ctx,
		cancel:              cancel,
		pendingStreams:      make(map[string]int),
		engineErrors:        make(map[string]*engineErrorState),
	}

	// Mark the second engine as recovering
	client.engineErrors["recovering-engine"] = &engineErrorState{
		failCount:     engineFailureThreshold,
		recovering:    true,
		recoveryStart: time.Now(),
	}

	// Select best engine
	host, port, containerID, err := client.SelectBestEngine()
	if err != nil {
		t.Fatalf("Failed to select engine: %v", err)
	}

	// Should select the healthy engine, not the recovering one
	if containerID != "healthy-engine" {
		t.Errorf("Expected to select healthy-engine, got %s", containerID)
	}

	if host != "localhost" || port != 19000 {
		t.Errorf("Expected localhost:19000, got %s:%d", host, port)
	}
}

// TestSelectBestEngine_AllRecovering tests behavior when all engines are recovering
func TestSelectBestEngine_AllRecovering(t *testing.T) {
	now := time.Now()
	var provisionCalled bool
	var mu sync.Mutex
	
	// Create mock server for orchestrator
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/engines" {
			// Return two engines
			engines := []engineState{
				{
					ContainerID:     "recovering-engine-1",
					Host:            "localhost",
					Port:            19000,
					HealthStatus:    "healthy",
					LastHealthCheck: now,
					LastStreamUsage: now.Add(-10 * time.Minute),
				},
				{
					ContainerID:     "recovering-engine-2",
					Host:            "localhost",
					Port:            19001,
					HealthStatus:    "healthy",
					LastHealthCheck: now,
					LastStreamUsage: now.Add(-5 * time.Minute),
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(engines)
			return
		}
		if r.URL.Path == "/streams" {
			// Return no streams - both engines are empty
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]streamState{})
			return
		}
		if r.URL.Path == "/orchestrator/status" {
			// Allow provisioning
			status := orchestratorStatus{
				Status: "healthy",
				Provisioning: struct {
					CanProvision         bool            `json:"can_provision"`
					BlockedReason        string          `json:"blocked_reason"`
					BlockedReasonDetails *ProvisionError `json:"blocked_reason_details"`
				}{
					CanProvision: true,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(status)
			return
		}
		if r.URL.Path == "/provision/acestream" && r.Method == http.MethodPost {
			mu.Lock()
			provisionCalled = true
			mu.Unlock()
			
			// Return a new provisioned engine
			resp := aceProvisionResponse{
				ContainerID:   "new-provisioned-engine",
				ContainerName: "new-engine",
				HostHTTPPort:  19002,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}
		t.Logf("Unexpected request to %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := &orchClient{
		base:                server.URL,
		maxStreamsPerEngine: 1,
		hc:                  &http.Client{Timeout: 3 * time.Second},
		ctx:                 ctx,
		cancel:              cancel,
		pendingStreams:      make(map[string]int),
		engineErrors:        make(map[string]*engineErrorState),
	}

	// Mark both engines as recovering
	client.engineErrors["recovering-engine-1"] = &engineErrorState{
		failCount:     engineFailureThreshold,
		recovering:    true,
		recoveryStart: time.Now(),
	}
	client.engineErrors["recovering-engine-2"] = &engineErrorState{
		failCount:     engineFailureThreshold,
		recovering:    true,
		recoveryStart: time.Now(),
	}

	// Update health to allow provisioning
	client.health.canProvision = true

	// Should provision a new engine since all are recovering
	host, port, containerID, err := client.SelectBestEngine()
	if err != nil {
		t.Fatalf("Expected provisioning to succeed when all engines recovering: %v", err)
	}

	// Should be a newly provisioned engine
	if containerID == "recovering-engine-1" || containerID == "recovering-engine-2" {
		t.Error("Should not select a recovering engine")
	}

	mu.Lock()
	wasProvisionCalled := provisionCalled
	mu.Unlock()

	if !wasProvisionCalled {
		t.Error("Expected provisioning to be called when all engines are recovering")
	}

	t.Logf("Provisioned new engine: host=%s port=%d containerID=%s", host, port, containerID)
}

// TestMultipleEngineFailures tests tracking failures across multiple engines
func TestMultipleEngineFailures(t *testing.T) {
	client := &orchClient{
		engineErrors: make(map[string]*engineErrorState),
	}

	// Record failures for multiple engines
	for i := 0; i < 3; i++ {
		client.RecordEngineFailure("engine-1")
	}

	for i := 0; i < engineFailureThreshold; i++ {
		client.RecordEngineFailure("engine-2")
	}

	for i := 0; i < 2; i++ {
		client.RecordEngineFailure("engine-3")
	}

	// Check states
	client.engineErrorsMu.RLock()
	state1 := client.engineErrors["engine-1"]
	state2 := client.engineErrors["engine-2"]
	state3 := client.engineErrors["engine-3"]
	client.engineErrorsMu.RUnlock()

	// Engine 1: 3 failures, not recovering
	if state1 == nil || state1.failCount != 3 || state1.recovering {
		t.Errorf("Engine 1 state incorrect: %+v", state1)
	}

	// Engine 2: threshold failures, recovering
	if state2 == nil || state2.failCount != engineFailureThreshold || !state2.recovering {
		t.Errorf("Engine 2 state incorrect: %+v", state2)
	}

	// Engine 3: 2 failures, not recovering
	if state3 == nil || state3.failCount != 2 || state3.recovering {
		t.Errorf("Engine 3 state incorrect: %+v", state3)
	}
}

// TestRecoveryPeriodTransition tests the transition from recovering to recovered
func TestRecoveryPeriodTransition(t *testing.T) {
	client := &orchClient{
		engineErrors: make(map[string]*engineErrorState),
	}

	containerID := "test-engine"

	// Mark engine as recovering with a start time that will expire soon
	client.engineErrors[containerID] = &engineErrorState{
		failCount:     engineFailureThreshold,
		recovering:    true,
		recoveryStart: time.Now().Add(-engineRecoveryPeriod + 2*time.Second), // Will expire in 2 seconds
	}

	// Should be recovering now
	if !client.IsEngineRecovering(containerID) {
		t.Error("Engine should be recovering")
	}

	// Wait for recovery period to expire
	time.Sleep(3 * time.Second)

	// Should not be recovering anymore
	if client.IsEngineRecovering(containerID) {
		t.Error("Engine should have recovered after period expired")
	}

	// State should be cleaned up
	client.engineErrorsMu.RLock()
	_, exists := client.engineErrors[containerID]
	client.engineErrorsMu.RUnlock()

	if exists {
		t.Error("Error state should be cleaned up")
	}
}

// TestNilClient tests that nil client doesn't cause panics
func TestNilClient_RecoveryMethods(t *testing.T) {
	var client *orchClient

	// These should not panic
	client.RecordEngineFailure("test")
	recovering := client.IsEngineRecovering("test")
	client.ResetEngineErrors("test")

	if recovering {
		t.Error("Nil client should return false for IsEngineRecovering")
	}
}

// TestEmptyContainerID tests handling of empty container IDs
func TestEmptyContainerID_RecoveryMethods(t *testing.T) {
	client := &orchClient{
		engineErrors: make(map[string]*engineErrorState),
	}

	// These should handle empty strings gracefully
	client.RecordEngineFailure("")
	recovering := client.IsEngineRecovering("")
	client.ResetEngineErrors("")

	if recovering {
		t.Error("Empty container ID should return false for IsEngineRecovering")
	}

	// Map should remain empty
	if len(client.engineErrors) != 0 {
		t.Error("Error map should be empty for empty container IDs")
	}
}
