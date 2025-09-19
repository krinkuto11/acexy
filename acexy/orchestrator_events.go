package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"time"
)

type orchClient struct {
	base string
	key  string
	hc   *http.Client
	// opcional si el proxy conoce el contenedor
	containerID string
}

func newOrchClient(base string) *orchClient {
	if base == "" {
		return nil
	}
	return &orchClient{
		base:        base,
		key:         os.Getenv("ACEXY_ORCH_APIKEY"),
		containerID: os.Getenv("ACEXY_CONTAINER_ID"),
		hc:          &http.Client{Timeout: 3 * time.Second},
	}
}

type startedEvent struct {
	ContainerID string `json:"container_id,omitempty"`
	Engine      struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	} `json:"engine"`
	Stream struct {
		KeyType string `json:"key_type"`
		Key     string `json:"key"`
	} `json:"stream"`
	Session struct {
		PlaybackSessionID string `json:"playback_session_id"`
		StatURL           string `json:"stat_url"`
		CommandURL        string `json:"command_url"`
		IsLive            int    `json:"is_live"`
	} `json:"session"`
	Labels map[string]string `json:"labels,omitempty"`
}

type endedEvent struct {
	ContainerID string `json:"container_id,omitempty"`
	StreamID    string `json:"stream_id,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

func (c *orchClient) post(path string, body any) {
	if c == nil {
		return
	}
	b, err := json.Marshal(body)
	if err != nil {
		slog.Warn("Failed to marshal orchestrator event", "error", err, "path", path)
		return
	}

	req, err := http.NewRequest(http.MethodPost, c.base+path, bytes.NewReader(b))
	if err != nil {
		slog.Warn("Failed to create orchestrator request", "error", err, "path", path)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}

	go func() {
		slog.Debug("Sending event to orchestrator", "url", c.base+path)
		resp, err := c.hc.Do(req)
		if err != nil {
			slog.Warn("Failed to send event to orchestrator", "error", err, "url", c.base+path)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			slog.Warn("Orchestrator returned error status", "status", resp.StatusCode, "url", c.base+path)
		} else {
			slog.Debug("Successfully sent event to orchestrator", "status", resp.StatusCode, "url", c.base+path)
		}
	}()
}

func (c *orchClient) EmitStarted(host string, port int, keyType, key, playbackID, statURL, cmdURL, streamID string) {
	if c == nil {
		return
	}

	ev := startedEvent{ContainerID: c.containerID}
	ev.Engine.Host, ev.Engine.Port = host, port
	ev.Stream.KeyType, ev.Stream.Key = keyType, key
	ev.Session.PlaybackSessionID = playbackID
	ev.Session.StatURL, ev.Session.CommandURL = statURL, cmdURL
	ev.Session.IsLive = 1
	ev.Labels = map[string]string{"stream_id": streamID}

	// Add debug logging for orchestrator integration
	slog.Debug("Emitting stream_started event to orchestrator",
		"stream_id", streamID, "key_type", keyType, "key", key,
		"host", host, "port", port, "playback_id", playbackID)

	c.post("/events/stream_started", ev)
}

func (c *orchClient) EmitEnded(streamID, reason string) {
	if c == nil {
		return
	}

	ev := endedEvent{ContainerID: c.containerID, StreamID: streamID, Reason: reason}

	// Add debug logging for orchestrator integration
	slog.Debug("Emitting stream_ended event to orchestrator",
		"stream_id", streamID, "reason", reason, "container_id", c.containerID)

	c.post("/events/stream_ended", ev)
}
