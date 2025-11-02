# Stream Failure Handling

This document describes the comprehensive failure handling mechanisms implemented to prevent engine saturation and system degradation.

## Problem Statement

When streams fail to start due to connection issues, timeouts, or other errors, the system can experience:

1. **Pending Stream Accumulation**: Failed stream attempts leave "phantom" pending streams that block engine capacity
2. **Retry Storms**: Multiple rapid retries overwhelm already-struggling engines
3. **Cascading Failures**: One failing engine causes load to shift to others, potentially cascading
4. **System Hangs**: Accumulation of these issues leads to complete system degradation

## Solution Architecture

### 1. Pending Stream Management

**Problem**: When `FetchStream` fails, pending stream counters were never released, causing engines to appear full.

**Solution**: 
- Release pending streams immediately on any failure path
- Periodic cleanup (every 5 minutes) clears any orphaned pending streams
- Explicit tracking ensures counters stay accurate

### 2. Circuit Breaker Pattern

**Problem**: Failing engines continue to receive requests, compounding failures.

**Solution**: Implement per-engine circuit breaker with three states:
- **Closed** (normal): All requests allowed
- **Open** (failing): Block all requests after threshold
- **Half-Open** (recovering): Allow retry after cooldown

**Configuration**:
```go
maxConsecutiveFails: 3         // Open circuit after 3 consecutive failures
cooldownPeriod: 60s            // Keep circuit open for 60 seconds
```

**Behavior**:
1. Track consecutive failures per engine
2. Open circuit after 3 consecutive failures
3. Block all requests for 60 seconds
4. Automatically allow retry after cooldown
5. Reset counter on first success

### 3. Rate Limiting

**Problem**: Too many concurrent stream start attempts overwhelm engines.

**Solution**: Limit concurrent attempts per engine.

**Configuration**:
```go
maxConcurrentPerEngine: 5      // Max 5 concurrent stream starts per engine
```

**Behavior**:
1. Track active attempts per engine
2. Reject new attempts when at capacity
3. Release slot when attempt completes (success or failure)
4. Return 503 Service Unavailable to client

### 4. Failure Tracking

**Problem**: No visibility into engine health and failure patterns.

**Solution**: Comprehensive per-engine health metrics:
- Consecutive failures (for circuit breaker)
- Total failures (historical)
- Total attempts (historical)
- Active concurrent attempts (for rate limiting)
- Circuit breaker state

### 5. Automatic Cleanup

**Problem**: Tracking data can accumulate unbounded.

**Solution**: Automatic cleanup routines:
- Pending streams: Cleared every 5 minutes
- Ended streams: Cleared when exceeding 1000 entries
- Failure tracking: Entries removed after 10 minutes of inactivity

## Implementation Details

### Circuit Breaker State Machine

```
[Closed] --3 failures--> [Open] --60s cooldown--> [Half-Open] --success--> [Closed]
                           |                           |
                           +--60s cooldown--> [Open] <-+--failure
```

### Request Flow

```
1. Client Request
   ↓
2. Select Engine (orchestrator)
   ↓
3. Check Circuit Breaker ← Can attempt?
   ↓ Yes
4. Check Rate Limit ← Slot available?
   ↓ Yes
5. Record Attempt (increment active)
   ↓
6. Try FetchStream
   ↓
7a. Success          7b. Failure
    ↓                    ↓
    Record Success       Record Failure
    ↓                    ↓
    Reset Circuit        Increment Consecutive
    ↓                    ↓
8. Release Slot      8. Release Slot
   ↓                    ↓
9. Continue          9. Return Error (503)
```

## Monitoring

### Log Messages

**Circuit Breaker Events**:
```
WARN Engine circuit breaker open engine=<id> reason=circuit breaker open due to consecutive failures
```

**Failure Recording**:
```
WARN Engine failure recorded engine=<id> consecutive_failures=<n> total_failures=<n> total_attempts=<n> circuit_open=<bool>
```

**Rate Limiting**:
```
WARN Engine at max concurrent attempts engine=<id>
```

**Cleanup**:
```
WARN Clearing stale pending streams count=<n> engines=<map>
```

### Health Check Queries

Query engine health programmatically:
```go
consecutive, total, attempts, circuitOpen := failureTracker.GetEngineHealth(engineID)
```

## Configuration

All failure handling is configured with sensible defaults and cannot be disabled (by design for system protection).

Current defaults:
- **Circuit Breaker**: 3 consecutive failures trigger 60s cooldown
- **Rate Limit**: 5 concurrent attempts per engine
- **Cleanup Interval**: 5 minutes
- **Stale Data Threshold**: 10 minutes inactive

To adjust these values, modify the `NewEngineFailureTracker()` function in `engine_failure_tracker.go`.

## Error Responses

Clients receive appropriate HTTP status codes:

| Scenario | Status Code | Message |
|----------|-------------|---------|
| Circuit Open | 503 | "Service temporarily unavailable: Engine is recovering from failures" |
| Rate Limited | 503 | "Service temporarily unavailable: Engine is busy" |
| FetchStream Failed | 500 | "Failed to start stream: <error>" |
| StartStream Failed | 500 | "Failed to start stream: <error>" |

## Testing

Comprehensive test coverage includes:
- Circuit breaker opens after 3 failures
- Circuit breaker resets on success
- Circuit breaker auto-recovers after cooldown
- Rate limiting enforces concurrent limits
- Concurrent access safety
- Automatic cleanup of stale data

Run tests:
```bash
go test -v -run "TestCircuitBreaker|TestRateLimiting|TestCleanup"
```

## Impact on System Performance

Expected improvements:
1. **Reduced Engine Saturation**: Circuit breaker prevents overwhelming failing engines
2. **Faster Failure Detection**: Circuit opens after 3 failures vs unlimited retries
3. **Better Resource Utilization**: Rate limiting prevents resource exhaustion
4. **Improved Recovery**: Auto-cleanup prevents accumulation of stale state
5. **Graceful Degradation**: System continues operating with reduced capacity vs complete hang

## Future Enhancements

Potential improvements:
1. Configurable thresholds via environment variables
2. Adaptive timeouts based on engine performance
3. Health metrics endpoint for monitoring dashboards
4. Failure pattern analysis and alerting
5. Per-stream-type circuit breakers
