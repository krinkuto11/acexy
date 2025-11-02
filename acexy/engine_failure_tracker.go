package main

import (
	"sync"
	"time"
)

// EngineFailureTracker tracks failure rates and implements circuit breaker pattern
// to prevent saturating engines with failing stream attempts
type EngineFailureTracker struct {
	mu                  sync.RWMutex
	failures            map[string]*engineFailureState
	maxConsecutiveFails int
	failureWindow       time.Duration
	cooldownPeriod      time.Duration
	maxConcurrentPerEngine int // Maximum concurrent stream start attempts per engine
}

type engineFailureState struct {
	consecutiveFailures int
	lastFailureTime     time.Time
	circuitOpen         bool
	circuitOpenedAt     time.Time
	totalFailures       int
	totalAttempts       int
	activeAttempts      int // Track concurrent attempts to prevent overwhelming an engine
}

// NewEngineFailureTracker creates a new failure tracker
func NewEngineFailureTracker() *EngineFailureTracker {
	return &EngineFailureTracker{
		failures:               make(map[string]*engineFailureState),
		maxConsecutiveFails:    3,                 // Open circuit after 3 consecutive failures
		failureWindow:          30 * time.Second,  // Reset consecutive count after 30s success
		cooldownPeriod:         60 * time.Second,  // Keep circuit open for 60s
		maxConcurrentPerEngine: 5,                 // Allow max 5 concurrent stream starts per engine
	}
}

// RecordAttempt records a stream start attempt for an engine
// Returns false if the engine is at max concurrent attempts
func (t *EngineFailureTracker) RecordAttempt(engineID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	state, exists := t.failures[engineID]
	if !exists {
		state = &engineFailureState{}
		t.failures[engineID] = state
	}
	
	// Check if engine is at max concurrent attempts
	if state.activeAttempts >= t.maxConcurrentPerEngine {
		return false
	}
	
	state.totalAttempts++
	state.activeAttempts++
	return true
}

// ReleaseAttempt releases an active attempt slot for an engine
func (t *EngineFailureTracker) ReleaseAttempt(engineID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	state, exists := t.failures[engineID]
	if !exists {
		return
	}
	
	if state.activeAttempts > 0 {
		state.activeAttempts--
	}
}

// RecordSuccess records a successful stream start for an engine
func (t *EngineFailureTracker) RecordSuccess(engineID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	state, exists := t.failures[engineID]
	if !exists {
		return
	}

	// Reset consecutive failures on success
	state.consecutiveFailures = 0
	state.circuitOpen = false
}

// RecordFailure records a failed stream start attempt for an engine
func (t *EngineFailureTracker) RecordFailure(engineID string, reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	state, exists := t.failures[engineID]
	if !exists {
		state = &engineFailureState{}
		t.failures[engineID] = state
	}

	state.consecutiveFailures++
	state.totalFailures++
	state.lastFailureTime = time.Now()

	// Open circuit breaker if too many consecutive failures
	if state.consecutiveFailures >= t.maxConsecutiveFails {
		state.circuitOpen = true
		state.circuitOpenedAt = time.Now()
	}
}

// CanAttempt checks if we can attempt to start a stream on this engine
// Returns false if circuit breaker is open
func (t *EngineFailureTracker) CanAttempt(engineID string) (bool, string) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	state, exists := t.failures[engineID]
	if !exists {
		return true, ""
	}

	// Check if circuit breaker is open
	if state.circuitOpen {
		// Check if cooldown period has passed
		if time.Since(state.circuitOpenedAt) < t.cooldownPeriod {
			return false, "circuit breaker open due to consecutive failures"
		}
		// Cooldown period passed, transition to half-open (allow retry)
		// Circuit will be fully closed on next success, or re-opened on failure
	}

	return true, ""
}

// GetEngineHealth returns health information for an engine
func (t *EngineFailureTracker) GetEngineHealth(engineID string) (consecutive int, total int, attempts int, circuitOpen bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	state, exists := t.failures[engineID]
	if !exists {
		return 0, 0, 0, false
	}

	return state.consecutiveFailures, state.totalFailures, state.totalAttempts, state.circuitOpen
}

// Cleanup removes stale failure tracking data
func (t *EngineFailureTracker) Cleanup() {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	for engineID, state := range t.failures {
		// Only remove entries that have recorded failures and have been inactive for 10 minutes
		// Skip engines with no failure history (lastFailureTime is zero)
		if !state.lastFailureTime.IsZero() && now.Sub(state.lastFailureTime) > 10*time.Minute {
			delete(t.failures, engineID)
		}
	}
}

// StartCleanupMonitor starts a background cleanup routine
func (t *EngineFailureTracker) StartCleanupMonitor(stopChan <-chan struct{}) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			t.Cleanup()
		}
	}
}
