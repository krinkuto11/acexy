// Acexy - Copyright (C) 2024 - Javinator9889 <dev at javinator9889 dot com>
// This program comes with ABSOLUTELY NO WARRANTY; for details type `show w'.
// This is free software, and you are welcome to redistribute it
// under certain conditions; type `show c' for details.
package acexy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// TestCleanupUnstartedStream verifies that streams are cleaned up when fetched but not started
func TestCleanupUnstartedStream(t *testing.T) {
	// Create a mock acestream engine
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ace/getstream" {
			w.Header().Set("Content-Type", "application/json")
			streamID := r.URL.Query().Get("id")
			w.Write([]byte(fmt.Sprintf(`{
				"response": {
					"playback_url": "http://localhost:9999/stream/%s",
					"stat_url": "http://localhost:6878/ace/stat/%s/playback123",
					"command_url": "http://localhost:6878/ace/cmd/%s/playback123"
				}
			}`, streamID, streamID, streamID)))
			return
		}
		// Handle command URL for cleanup
		if r.URL.Path == "/ace/cmd/stream1/playback123" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"response": "stopped"}`))
			return
		}
	}))
	defer engine.Close()

	// Parse engine URL
	engineURL, _ := url.Parse(engine.URL)

	// Create an Acexy instance
	acexy := &Acexy{
		Scheme:            "http",
		Host:              engineURL.Hostname(),
		Port:              mustParsePortTest(t, engineURL.Port()),
		Endpoint:          MPEG_TS_ENDPOINT,
		EmptyTimeout:      10 * time.Second,
		BufferSize:        1024,
		NoResponseTimeout: 5 * time.Second,
	}
	acexy.Init()

	// Fetch a stream but don't start it
	aceId, _ := NewAceID("stream1", "")
	stream, err := acexy.FetchStream(aceId, nil)
	if err != nil {
		t.Fatalf("Failed to fetch stream: %v", err)
	}

	// Verify the stream is in the map
	if len(acexy.streams) != 1 {
		t.Errorf("Expected 1 stream in map, got %d", len(acexy.streams))
	}

	// Now cleanup the unstarted stream
	acexy.CleanupUnstartedStream(aceId)

	// Verify the stream is removed
	if len(acexy.streams) != 0 {
		t.Errorf("Expected 0 streams after cleanup, got %d", len(acexy.streams))
	}

	t.Logf("Successfully cleaned up unstarted stream: %s", stream.ID)
}

// TestIdleStreamCleanupByAge verifies that idle streams are cleaned up based on age
func TestIdleStreamCleanupByAge(t *testing.T) {
	// Create a mock acestream engine
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ace/getstream" {
			w.Header().Set("Content-Type", "application/json")
			streamID := r.URL.Query().Get("id")
			w.Write([]byte(fmt.Sprintf(`{
				"response": {
					"playback_url": "http://localhost:9999/stream/%s",
					"stat_url": "http://localhost:6878/ace/stat/%s/playback123",
					"command_url": "http://localhost:6878/ace/cmd/%s/playback123"
				}
			}`, streamID, streamID, streamID)))
			return
		}
	}))
	defer engine.Close()

	// Parse engine URL
	engineURL, _ := url.Parse(engine.URL)

	// Create an Acexy instance
	acexy := &Acexy{
		Scheme:            "http",
		Host:              engineURL.Hostname(),
		Port:              mustParsePortTest(t, engineURL.Port()),
		Endpoint:          MPEG_TS_ENDPOINT,
		EmptyTimeout:      10 * time.Second,
		BufferSize:        1024,
		NoResponseTimeout: 5 * time.Second,
	}
	acexy.Init()

	// Fetch a stream but don't start it
	aceId, _ := NewAceID("stream1", "")
	_, err := acexy.FetchStream(aceId, nil)
	if err != nil {
		t.Fatalf("Failed to fetch stream: %v", err)
	}

	// Verify the stream is in the map
	if len(acexy.streams) != 1 {
		t.Errorf("Expected 1 stream in map, got %d", len(acexy.streams))
	}

	// Try to fetch the same stream again immediately - should reuse it
	_, err = acexy.FetchStream(aceId, nil)
	if err != nil {
		t.Fatalf("Failed to refetch stream: %v", err)
	}

	// Should still have 1 stream (reused)
	if len(acexy.streams) != 1 {
		t.Errorf("Expected 1 stream after immediate refetch, got %d", len(acexy.streams))
	}

	// Manually age the stream by modifying its createdAt timestamp
	acexy.mutex.Lock()
	if stream, ok := acexy.streams[aceId]; ok {
		stream.createdAt = time.Now().Add(-35 * time.Second) // Make it older than 30 seconds
	}
	acexy.mutex.Unlock()

	// Try to fetch again - should clean up the old one and create new
	_, err = acexy.FetchStream(aceId, nil)
	if err != nil {
		t.Fatalf("Failed to refetch aged stream: %v", err)
	}

	// Should still have 1 stream (old one cleaned, new one created)
	if len(acexy.streams) != 1 {
		t.Errorf("Expected 1 stream after aged refetch, got %d", len(acexy.streams))
	}

	// Verify the stream has a recent createdAt time
	acexy.mutex.Lock()
	if stream, ok := acexy.streams[aceId]; ok {
		age := time.Since(stream.createdAt)
		if age > 5*time.Second {
			t.Errorf("Expected new stream to be fresh, but it's %v old", age)
		}
	}
	acexy.mutex.Unlock()

	t.Logf("Successfully tested idle stream cleanup by age")
}

// TestStaleStreamCleanupGoroutine verifies the background cleanup goroutine works
func TestStaleStreamCleanupGoroutine(t *testing.T) {
	// Create a mock acestream engine
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ace/getstream" {
			w.Header().Set("Content-Type", "application/json")
			streamID := r.URL.Query().Get("id")
			w.Write([]byte(fmt.Sprintf(`{
				"response": {
					"playback_url": "http://localhost:9999/stream/%s",
					"stat_url": "http://localhost:6878/ace/stat/%s/playback123",
					"command_url": "http://localhost:6878/ace/cmd/%s/playback123"
				}
			}`, streamID, streamID, streamID)))
			return
		}
	}))
	defer engine.Close()

	// Parse engine URL
	engineURL, _ := url.Parse(engine.URL)

	// Create an Acexy instance
	acexy := &Acexy{
		Scheme:            "http",
		Host:              engineURL.Hostname(),
		Port:              mustParsePortTest(t, engineURL.Port()),
		Endpoint:          MPEG_TS_ENDPOINT,
		EmptyTimeout:      10 * time.Second,
		BufferSize:        1024,
		NoResponseTimeout: 5 * time.Second,
	}
	acexy.Init()

	// Fetch a stream but don't start it
	aceId, _ := NewAceID("stream1", "")
	_, err := acexy.FetchStream(aceId, nil)
	if err != nil {
		t.Fatalf("Failed to fetch stream: %v", err)
	}

	// Verify the stream is in the map
	if len(acexy.streams) != 1 {
		t.Errorf("Expected 1 stream in map, got %d", len(acexy.streams))
	}

	// Manually age the stream to be older than 5 minutes
	acexy.mutex.Lock()
	if stream, ok := acexy.streams[aceId]; ok {
		stream.createdAt = time.Now().Add(-6 * time.Minute)
	}
	acexy.mutex.Unlock()

	// Wait for the cleanup goroutine to run (it runs every 1 minute)
	// We'll wait up to 70 seconds to be safe
	cleaned := false
	for i := 0; i < 14; i++ {
		time.Sleep(5 * time.Second)
		acexy.mutex.Lock()
		streamsCount := len(acexy.streams)
		acexy.mutex.Unlock()
		
		if streamsCount == 0 {
			cleaned = true
			break
		}
	}

	if !cleaned {
		t.Errorf("Expected stream to be cleaned up by background goroutine, but it's still present")
	}

	t.Logf("Successfully tested background cleanup goroutine")
}

// TestClientDisconnectDuringSetup simulates a client disconnecting between FetchStream and StartStream
func TestClientDisconnectDuringSetup(t *testing.T) {
	// Create a mock acestream engine
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ace/getstream" {
			w.Header().Set("Content-Type", "application/json")
			streamID := r.URL.Query().Get("id")
			w.Write([]byte(fmt.Sprintf(`{
				"response": {
					"playback_url": "http://localhost:9999/stream/%s",
					"stat_url": "http://localhost:6878/ace/stat/%s/playback123",
					"command_url": "http://localhost:6878/ace/cmd/%s/playback123"
				}
			}`, streamID, streamID, streamID)))
			return
		}
		// Handle command URL for cleanup
		if r.URL.Path == "/ace/cmd/stream1/playback123" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"response": "stopped"}`))
			return
		}
	}))
	defer engine.Close()

	// Parse engine URL
	engineURL, _ := url.Parse(engine.URL)

	// Create an Acexy instance
	acexy := &Acexy{
		Scheme:            "http",
		Host:              engineURL.Hostname(),
		Port:              mustParsePortTest(t, engineURL.Port()),
		Endpoint:          MPEG_TS_ENDPOINT,
		EmptyTimeout:      10 * time.Second,
		BufferSize:        1024,
		NoResponseTimeout: 5 * time.Second,
	}
	acexy.Init()

	// Create a context that we'll cancel
	ctx, cancel := context.WithCancel(context.Background())

	// Fetch a stream
	aceId, _ := NewAceID("stream1", "")
	_, err := acexy.FetchStream(aceId, nil)
	if err != nil {
		t.Fatalf("Failed to fetch stream: %v", err)
	}

	// Verify the stream is in the map
	if len(acexy.streams) != 1 {
		t.Errorf("Expected 1 stream in map after fetch, got %d", len(acexy.streams))
	}

	// Cancel the context (simulating client disconnect)
	cancel()

	// Check that context is done
	select {
	case <-ctx.Done():
		t.Log("Context cancelled successfully")
	default:
		t.Error("Context should be cancelled")
	}

	// Clean up the stream as would happen in the proxy
	acexy.CleanupUnstartedStream(aceId)

	// Verify the stream is removed
	if len(acexy.streams) != 0 {
		t.Errorf("Expected 0 streams after client disconnect cleanup, got %d", len(acexy.streams))
	}

	t.Logf("Successfully handled client disconnect during setup")
}

func mustParsePortTest(t *testing.T, port string) int {
	var p int
	_, err := fmt.Sscanf(port, "%d", &p)
	if err != nil {
		t.Fatalf("Failed to parse port %s: %v", port, err)
	}
	return p
}
