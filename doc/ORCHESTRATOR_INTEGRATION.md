# acexy Orchestrator Integration

This document explains how acexy integrates with the acestream-orchestrator for dynamic load balancing and engine management.

## Overview

The orchestrator integration allows acexy to:

1. **Dynamically select engines** instead of using a fixed host/port configuration
2. **Enforce load balancing** with a single stream per engine constraint
3. **Automatically provision new engines** when demand exceeds available capacity
4. **Gracefully fallback** to configured engine when orchestrator is unavailable

## Architecture

```
[Client] -> [acexy] -> [Orchestrator] -> [AceStream Engines]
                   \-> [Fallback Engine] (if orchestrator unavailable)
```

### Flow

1. **Stream Request**: Client requests stream via acexy API
2. **Engine Selection**: acexy queries orchestrator for available engines
3. **Load Check**: For each engine, check active stream count
4. **Engine Choice**: 
   - Use engine with 0 active streams (single stream per engine)
   - If no available engines, provision new one via orchestrator
5. **Stream Serving**: Serve stream from selected engine
6. **Event Reporting**: Report stream start/end events to orchestrator

## Configuration

### Environment Variables

| Variable | Description | Required |
|----------|-------------|----------|
| `ACEXY_ORCH_URL` | Base URL for orchestrator API (e.g., `http://orchestrator:8000`) | Yes (for integration) |
| `ACEXY_ORCH_APIKEY` | API key if orchestrator requires authentication | No |
| `ACEXY_CONTAINER_ID` | Container ID for identification (auto-detected in Docker) | No |

### Fallback Configuration

When orchestrator integration is disabled or fails, acexy falls back to:

| Variable | Description | Default |
|----------|-------------|---------|
| `ACEXY_HOST` | Fallback AceStream host | `localhost` |
| `ACEXY_PORT` | Fallback AceStream port | `6878` |

## Load Balancing Algorithm

The load balancing implements a "single stream per engine" strategy:

1. **Query all engines** from orchestrator
2. **Check stream count** for each engine  
3. **Select first engine** with 0 active streams
4. **Provision new engine** if all engines are busy
5. **Report events** to orchestrator for tracking

### Engine Selection Logic

```go
for _, engine := range engines {
    activeStreams := countActiveStreams(engine)
    if activeStreams == 0 {
        return engine  // Use this engine
    }
}
// No available engines, provision new one
return provisionNewEngine()
```

## API Integration

### Orchestrator APIs Used

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/engines` | GET | List all available engines |
| `/streams?container_id={id}&status=started` | GET | Check active streams per engine |
| `/provision/acestream` | POST | Provision new acestream engine |
| `/events/stream_started` | POST | Report stream start event |
| `/events/stream_ended` | POST | Report stream end event |

### Event Reporting

acexy reports stream lifecycle events to the orchestrator:

```json
// Stream Started Event
{
  "engine": {"host": "localhost", "port": 19001},
  "stream": {"key_type": "content_id", "key": "abc123"},
  "session": {
    "playback_session_id": "sess_456",
    "stat_url": "http://localhost:19001/ace/stat/abc123/sess_456",
    "command_url": "http://localhost:19001/ace/stat/abc123/sess_456",
    "is_live": 1
  },
  "labels": {"stream_id": "abc123|sess_456"}
}

// Stream Ended Event  
{
  "stream_id": "abc123|sess_456",
  "reason": "handler_exit"
}
```

## Error Handling

### Orchestrator Unavailable

- acexy logs warning and falls back to configured `ACEXY_HOST:ACEXY_PORT`
- Stream requests continue to work in single-engine mode
- Events are not reported (gracefully ignored)

### Engine Provisioning Fails

- acexy returns 500 error to client
- Error is logged with details
- Orchestrator may retry or use different strategy

### Engine Connection Fails

- acexy reports stream failure event to orchestrator
- Returns error to client
- Orchestrator can mark engine as unhealthy

## Monitoring

### acexy Logs

- `INFO` level: Engine selection and orchestrator status
- `DEBUG` level: Detailed engine queries and event reporting
- `WARN` level: Orchestrator connection issues

### Orchestrator Integration

The orchestrator provides:

- Engine health monitoring
- Stream statistics and metrics
- Automatic container lifecycle management
- Load balancing insights

## Troubleshooting

### Common Issues

1. **Orchestrator connection timeout**
   - Check `ACEXY_ORCH_URL` is correct and reachable
   - Verify orchestrator is running and healthy
   - Check network connectivity between acexy and orchestrator

2. **API authentication failures**
   - Verify `ACEXY_ORCH_APIKEY` matches orchestrator configuration
   - Check orchestrator logs for authentication errors

3. **Engine provisioning failures**
   - Check Docker daemon is accessible to orchestrator
   - Verify sufficient resources (ports, memory, CPU)
   - Check orchestrator configuration (image, network, etc.)

4. **Load balancing not working**
   - Verify streams are being reported to orchestrator
   - Check orchestrator `/streams` endpoint shows active streams
   - Enable DEBUG logging to see engine selection process

### Debug Commands

```bash
# Test orchestrator connectivity
curl -H "Authorization: Bearer $API_KEY" $ORCH_URL/engines

# Check active streams
curl -H "Authorization: Bearer $API_KEY" $ORCH_URL/streams?status=started

# Enable debug logging
ACEXY_LOG_LEVEL=DEBUG ./acexy
```