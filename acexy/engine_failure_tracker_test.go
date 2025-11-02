package main

import (
	"testing"
	"time"
)

func TestCircuitBreakerOpensOnConsecutiveFailures(t *testing.T) {
	tracker := NewEngineFailureTracker()
	engineID := "test-engine-1"

	// First 2 failures should not open circuit
	for i := 0; i < 2; i++ {
		tracker.RecordAttempt(engineID)
		tracker.RecordFailure(engineID, "test failure")
		tracker.ReleaseAttempt(engineID)
		
		canAttempt, _ := tracker.CanAttempt(engineID)
		if !canAttempt {
			t.Errorf("Circuit breaker should not be open after %d failures", i+1)
		}
	}

	// Third failure should open circuit
	tracker.RecordAttempt(engineID)
	tracker.RecordFailure(engineID, "test failure")
	tracker.ReleaseAttempt(engineID)
	
	canAttempt, reason := tracker.CanAttempt(engineID)
	if canAttempt {
		t.Error("Circuit breaker should be open after 3 consecutive failures")
	}
	if reason == "" {
		t.Error("Circuit breaker should provide a reason")
	}

	consecutive, total, attempts, circuitOpen := tracker.GetEngineHealth(engineID)
	if consecutive != 3 {
		t.Errorf("Expected 3 consecutive failures, got %d", consecutive)
	}
	if total != 3 {
		t.Errorf("Expected 3 total failures, got %d", total)
	}
	if attempts != 3 {
		t.Errorf("Expected 3 total attempts, got %d", attempts)
	}
	if !circuitOpen {
		t.Error("Circuit should be reported as open")
	}

	t.Log("Circuit breaker correctly opens after 3 consecutive failures")
}

func TestCircuitBreakerResetsOnSuccess(t *testing.T) {
	tracker := NewEngineFailureTracker()
	engineID := "test-engine-1"

	// Record 2 failures
	for i := 0; i < 2; i++ {
		tracker.RecordAttempt(engineID)
		tracker.RecordFailure(engineID, "test failure")
		tracker.ReleaseAttempt(engineID)
	}

	consecutive, _, _, _ := tracker.GetEngineHealth(engineID)
	if consecutive != 2 {
		t.Errorf("Expected 2 consecutive failures, got %d", consecutive)
	}

	// Record a success
	tracker.RecordAttempt(engineID)
	tracker.RecordSuccess(engineID)
	tracker.ReleaseAttempt(engineID)

	consecutive, _, _, circuitOpen := tracker.GetEngineHealth(engineID)
	if consecutive != 0 {
		t.Errorf("Expected consecutive failures to reset to 0, got %d", consecutive)
	}
	if circuitOpen {
		t.Error("Circuit should be closed after success")
	}

	t.Log("Circuit breaker correctly resets on success")
}

func TestRateLimitingPreventsOverload(t *testing.T) {
	tracker := NewEngineFailureTracker()
	engineID := "test-engine-1"

	// Fill up to max concurrent attempts
	for i := 0; i < tracker.maxConcurrentPerEngine; i++ {
		if !tracker.RecordAttempt(engineID) {
			t.Errorf("Should accept attempt %d (max=%d)", i+1, tracker.maxConcurrentPerEngine)
		}
	}

	// Next attempt should be rejected
	if tracker.RecordAttempt(engineID) {
		t.Error("Should reject attempt when at max concurrent")
	}

	// Release one slot
	tracker.ReleaseAttempt(engineID)

	// Now should accept again
	if !tracker.RecordAttempt(engineID) {
		t.Error("Should accept attempt after releasing one")
	}

	t.Logf("Rate limiting correctly enforces max %d concurrent attempts", tracker.maxConcurrentPerEngine)
}

func TestCircuitBreakerCooldown(t *testing.T) {
	tracker := NewEngineFailureTracker()
	tracker.cooldownPeriod = 100 * time.Millisecond // Short cooldown for testing
	engineID := "test-engine-1"

	// Open circuit breaker
	for i := 0; i < 3; i++ {
		tracker.RecordAttempt(engineID)
		tracker.RecordFailure(engineID, "test failure")
		tracker.ReleaseAttempt(engineID)
	}

	// Verify circuit is open
	canAttempt, _ := tracker.CanAttempt(engineID)
	if canAttempt {
		t.Error("Circuit breaker should be open")
	}

	// Wait for cooldown
	time.Sleep(150 * time.Millisecond)

	// Circuit should allow retry after cooldown
	canAttempt, _ = tracker.CanAttempt(engineID)
	if !canAttempt {
		t.Error("Circuit breaker should allow retry after cooldown")
	}

	t.Log("Circuit breaker cooldown working correctly")
}

func TestCleanupRemovesStaleEntries(t *testing.T) {
	tracker := NewEngineFailureTracker()
	engineID := "test-engine-1"

	// Record a failure with modified timestamp
	tracker.RecordAttempt(engineID)
	tracker.RecordFailure(engineID, "test failure")
	tracker.ReleaseAttempt(engineID)
	
	// Manually set last failure time to be old
	tracker.mu.Lock()
	tracker.failures[engineID].lastFailureTime = time.Now().Add(-11 * time.Minute)
	tracker.mu.Unlock()

	// Run cleanup
	tracker.Cleanup()

	// Entry should be removed
	tracker.mu.RLock()
	_, exists := tracker.failures[engineID]
	tracker.mu.RUnlock()

	if exists {
		t.Error("Cleanup should remove stale entries")
	}

	t.Log("Cleanup correctly removes stale entries")
}

func TestConcurrentAccess(t *testing.T) {
	tracker := NewEngineFailureTracker()
	engineID := "test-engine-1"
	
	done := make(chan bool)
	
	// Simulate concurrent accesses
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				if tracker.RecordAttempt(engineID) {
					tracker.RecordSuccess(engineID)
					tracker.ReleaseAttempt(engineID)
				}
			}
			done <- true
		}()
	}
	
	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
	
	// Verify no concurrent access issues
	tracker.mu.RLock()
	state := tracker.failures[engineID]
	tracker.mu.RUnlock()
	
	if state.activeAttempts != 0 {
		t.Errorf("Expected 0 active attempts after completion, got %d", state.activeAttempts)
	}
	
	t.Log("Concurrent access handled correctly")
}

func TestGetEngineHealthNewEngine(t *testing.T) {
	tracker := NewEngineFailureTracker()
	engineID := "new-engine"

	consecutive, total, attempts, circuitOpen := tracker.GetEngineHealth(engineID)
	
	if consecutive != 0 || total != 0 || attempts != 0 || circuitOpen {
		t.Error("New engine should have clean health state")
	}

	t.Log("New engine health state initialized correctly")
}
