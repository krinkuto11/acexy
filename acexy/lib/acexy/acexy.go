// Acexy - Copyright (C) 2024 - Javinator9889 <dev at javinator9889 dot com>
// This program comes with ABSOLUTELY NO WARRANTY; for details type `show w'.
// This is free software, and you are welcome to redistribute it
// under certain conditions; type `show c' for details.
package acexy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"javinator9889/acexy/lib/pmw"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

// As of how the middleware is defined, we tell Go the structure that should match the HTTP
// response for AceStream: https://docs.acestream.net/developers/start-playback/#using-middleware.
// We are interested in the "playback_url" and the "command_url" fields: The first one
// references the stream, and the second one tells the stream to finish.
type AceStreamResponse struct {
	PlaybackURL       string `json:"playback_url"`
	StatURL           string `json:"stat_url"`
	CommandURL        string `json:"command_url"`
	Infohash          string `json:"infohash"`
	PlaybackSessionID string `json:"playback_session_id"`
	IsLive            int    `json:"is_live"`
	IsEncrypted       int    `json:"is_encrypted"`
	ClientSessionID   int    `json:"client_session_id"`
}

type AceStreamMiddleware struct {
	Response AceStreamResponse `json:"response"`
	Error    string            `json:"error"`
}

type AceStreamCommand struct {
	Response string `json:"response"`
	Error    string `json:"error"`
}

type AcexyStatus struct {
	Clients *uint  `json:"clients,omitempty"`
	Streams *uint  `json:"streams,omitempty"`
	ID      *AceID `json:"stream_id,omitempty"`
	StatURL string `json:"stat_url,omitempty"`
}

// The stream information is stored in a structure referencing the `AceStreamResponse`
// plus some extra information to determine whether we should keep the stream alive or not.
type AceStream struct {
	PlaybackURL string
	StatURL     string
	CommandURL  string
	ID          AceID
}

type ongoingStream struct {
	clients   uint
	done      chan struct{}
	player    *http.Response
	stream    *AceStream
	copier    *Copier
	writers   *pmw.PMultiWriter
	createdAt time.Time // Track when the stream was created
	failed    bool      // Track if the stream has failed during initialization
	// Engine information (set when using orchestrator)
	engineHost        string
	enginePort        int
	engineContainerID string
}

// Structure referencing the AceStream Proxy - this is, ourselves
type Acexy struct {
	Scheme            string        // The scheme to be used when connecting to the AceStream middleware
	Host              string        // The host to be used when connecting to the AceStream middleware
	Port              int           // The port to be used when connecting to the AceStream middleware
	Endpoint          AcexyEndpoint // The endpoint to be used when connecting to the AceStream middleware
	EmptyTimeout      time.Duration // Timeout after which, if no data is written, the stream is closed
	BufferSize        int           // The buffer size to use when copying the data
	NoResponseTimeout time.Duration // Timeout to wait for a response from the AceStream middleware

	// Information about ongoing streams
	streams    map[AceID]*ongoingStream
	mutex      *sync.Mutex
	middleware *http.Client
}

type AcexyEndpoint string

// The AceStream API available endpoints
const (
	M3U8_ENDPOINT    AcexyEndpoint = "/ace/manifest.m3u8"
	MPEG_TS_ENDPOINT AcexyEndpoint = "/ace/getstream"
)

// Initializes the Acexy structure
func (a *Acexy) Init() {
	a.streams = make(map[AceID]*ongoingStream)
	a.mutex = &sync.Mutex{}
	// The transport to be used when connecting to the AceStream middleware. We have to tweak it
	// a little bit to avoid compression and to limit the number of connections per host. Otherwise,
	// the AceStream Middleware won't work.
	a.middleware = &http.Client{
		Transport: &http.Transport{
			DisableCompression:    true,
			MaxIdleConns:          10,
			MaxConnsPerHost:       10,
			IdleConnTimeout:       30 * time.Second,
			ResponseHeaderTimeout: a.NoResponseTimeout,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}

	// Start a background goroutine to clean up stale streams
	go a.cleanupStaleStreams()
}

// cleanupStaleStreams runs in the background to clean up streams that may have gotten stuck
func (a *Acexy) cleanupStaleStreams() {
	ticker := time.NewTicker(1 * time.Minute) // Check every 1 minute
	defer ticker.Stop()

	for range ticker.C {
		a.mutex.Lock()
		staleCutoff := time.Now().Add(-5 * time.Minute) // Consider streams older than 5 minutes as potentially stale

		for aceId, stream := range a.streams {
			// Clean up streams that have no clients and no active player
			// More aggressive cleanup: check every minute and clean streams idle for 5+ minutes
			if stream.clients == 0 && stream.player == nil && stream.createdAt.Before(staleCutoff) {
				slog.Warn("Cleaning up stale stream", "stream", aceId, "created_at", stream.createdAt, "age", time.Since(stream.createdAt))
				delete(a.streams, aceId)

				// Close the done channel if not already closed
				select {
				case <-stream.done:
					// Already closed
				default:
					close(stream.done)
				}
			}
		}
		a.mutex.Unlock()
	}
}

// Starts a new stream. The stream is enqueued in the AceStream backend, returning a playback
// URL to reproduce it and a command URL to finish it. If the stream is already enqueued,
// the playback URL is returned. A number of clients can be reproducing the same stream at
// the same time through the middleware. When the last client finishes, the stream is removed.
// The stream is identified by the “id“ identifier. Optionally, takes extra parameters to
// customize the stream.
func (a *Acexy) FetchStream(aceId AceID, extraParams url.Values) (*AceStream, error) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	// Check if the stream is already enqueued
	if stream, ok := a.streams[aceId]; ok {
		// If stream has failed during initialization, clean it up immediately
		if stream.failed {
			slog.Debug("Cleaning up failed stream entry", "stream", aceId, "age", time.Since(stream.createdAt))
			delete(a.streams, aceId)
			// Close the done channel if not already closed
			select {
			case <-stream.done:
				// Already closed
			default:
				close(stream.done)
			}
			// Continue to create a new stream
		} else if stream.player == nil && stream.clients == 0 {
			// If stream has no clients and no player, it's in a broken or idle state
			// Clean it up if it's been idle for more than 30 seconds
			idleTime := time.Since(stream.createdAt)
			if idleTime > 30*time.Second {
				slog.Info("Cleaning up idle/broken stream entry", "stream", aceId, "age", idleTime)
				delete(a.streams, aceId)
				// Close the done channel if not already closed
				select {
				case <-stream.done:
					// Already closed
				default:
					close(stream.done)
				}
				// Continue to create a new stream
			} else {
				// Stream is still fresh, might be waiting for StartStream
				slog.Debug("Reusing recent unstarted stream", "stream", aceId, "age", idleTime)
				return stream.stream, nil
			}
		} else {
			slog.Info("Reusing existing active stream", "stream", aceId, "clients", stream.clients)
			return stream.stream, nil
		}
	}

	// Enqueue the middleware
	middleware, err := GetStream(a, aceId, extraParams)
	if err != nil {
		slog.Error("Error getting stream middleware", "error", err)
		// Ensure we don't leave any partial state in the streams map
		delete(a.streams, aceId)
		return nil, err
	}

	// We got the stream information, build the structure around it and register the stream
	slog.Debug("Middleware Information", "id", aceId, "middleware", middleware)
	stream := &AceStream{
		PlaybackURL: middleware.Response.PlaybackURL,
		StatURL:     middleware.Response.StatURL,
		CommandURL:  middleware.Response.CommandURL,
		ID:          aceId,
	}

	a.streams[aceId] = &ongoingStream{
		clients:   0,
		done:      make(chan struct{}),
		player:    nil,
		stream:    stream,
		writers:   pmw.New(),
		createdAt: time.Now(),
	}
	slog.Info("Started new stream", "id", aceId, "clients", a.streams[aceId].clients)
	return stream, nil
}

func (a *Acexy) StartStream(stream *AceStream, out io.Writer) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	// Get the ongoing stream
	ongoingStream, ok := a.streams[stream.ID]
	if !ok {
		slog.Debug("Stream not found", "stream", stream.ID)
		return fmt.Errorf(`stream "%s" not found`, stream.ID)
	}

	// Check if the stream has already failed
	if ongoingStream.failed {
		slog.Debug("Stream has failed, cannot start", "stream", stream.ID)
		return fmt.Errorf(`stream "%s" has failed`, stream.ID)
	}

	// Add the writer to the list of writers
	ongoingStream.writers.Add(out)

	// Register the new client
	ongoingStream.clients++

	// Check if the stream is already being played
	if ongoingStream.player != nil {
		return nil
	}

	resp, err := a.middleware.Get(stream.PlaybackURL)
	if err != nil {
		slog.Error("Failed to forward stream", "error", err)
		// Mark the stream as failed so it won't be reused
		ongoingStream.failed = true
		// Remove the writer that was just added since we're failing
		ongoingStream.writers.Remove(out)
		ongoingStream.clients--
		// Always try to release the stream on failure, even if clients > 0
		// This ensures we don't leave broken streams in the map
		if releaseErr := a.releaseStream(stream); releaseErr != nil {
			slog.Debug("Error releasing failed stream", "error", releaseErr)
			// Even if release fails, ensure the stream is removed from the map
			delete(a.streams, stream.ID)
		}
		return err
	}

	// Forward the response to the writers
	ongoingStream.copier = &Copier{
		Destination:  ongoingStream.writers,
		Source:       resp.Body,
		EmptyTimeout: a.EmptyTimeout,
		BufferSize:   a.BufferSize,
	}

	go func() {
		// Start copying the stream
		if err := ongoingStream.copier.Copy(); err != nil {
			if errors.Is(err, net.ErrClosed) {
				slog.Debug("Connection closed", "stream", stream.ID)
			} else {
				slog.Debug("Failed to copy response body", "stream", stream.ID, "error", err)
			}
		}
		slog.Debug("Copy done", "stream", stream.ID)
		select {
		case <-ongoingStream.done:
			slog.Debug("Stream already closed", "stream", stream.ID)
		default:
			close(ongoingStream.done)
			slog.Debug("Stream closed", "stream", stream.ID)
		}
	}()

	ongoingStream.player = resp
	return nil
}

// Releases a stream that is no longer being used. The stream is removed from the AceStream backend.
// If the stream is not enqueued, an error is returned. If the stream has clients reproducing it,
// the stream is not removed. The stream is identified by the “id“ identifier.
//
// Note: The global mutex is locked and unlocked by the caller.
func (a *Acexy) releaseStream(stream *AceStream) error {
	ongoingStream, ok := a.streams[stream.ID]
	if !ok {
		return fmt.Errorf(`stream "%s" not found`, stream.ID)
	}
	// Only allow release if no clients or if the stream has failed
	if ongoingStream.clients > 0 && !ongoingStream.failed {
		return fmt.Errorf(`stream "%s" has clients`, stream.ID)
	}

	// Remove the stream from the list first to prevent further access
	defer delete(a.streams, stream.ID)
	slog.Debug("Stopping stream", "stream", stream.ID)

	// Close the stream backend connection (don't fail the cleanup if this fails)
	if err := CloseStream(stream); err != nil {
		slog.Debug("Error closing stream backend (continuing cleanup anyway)", "error", err)
		// Don't return error here - we want to continue cleanup
	}

	// Close the player connection if it exists
	if ongoingStream.player != nil {
		slog.Debug("Closing player", "stream", stream.ID)
		if err := ongoingStream.player.Body.Close(); err != nil {
			slog.Debug("Error closing player body", "error", err)
		}
	}

	// Close the `done' channel
	select {
	case <-ongoingStream.done:
		slog.Debug("Stream already closed", "stream", stream.ID)
	default:
		close(ongoingStream.done)
		slog.Debug("Stream done", "stream", stream.ID)
	}
	return nil
}

// Finishes a stream. The stream is removed from the AceStream backend. If the stream is not
// enqueued, an error is returned. If the stream has clients reproducing it, the stream is not
// removed. The stream is identified by the “id“ identifier.
func (a *Acexy) StopStream(stream *AceStream, out io.Writer) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	// Get the ongoing stream
	ongoingStream, ok := a.streams[stream.ID]
	if !ok {
		slog.Debug("Stream not found", "stream", stream.ID)
		return fmt.Errorf(`stream "%s" not found`, stream.ID)
	}

	// Remove the writer from the list of writers
	ongoingStream.writers.Remove(out)

	// Unregister the client
	if ongoingStream.clients > 0 {
		ongoingStream.clients--
		slog.Info("Client stopped", "stream", stream.ID, "clients", ongoingStream.clients)
	} else {
		slog.Warn("Stream has no clients", "stream", stream.ID)
	}

	// Check if we have to stop the stream
	if ongoingStream.clients == 0 {
		if err := a.releaseStream(stream); err != nil {
			slog.Warn("Error releasing stream", "error", err)
			return err
		}
		slog.Info("Stream done", "stream", stream.ID)
	}
	return nil
}

// Waits for the stream to finish. The stream is identified by the “id“ identifier. If the stream
// is not enqueued, nil is returned. The function returns a channel that will be closed when the
// stream finishes.
func (a *Acexy) WaitStream(stream *AceStream) <-chan struct{} {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	// Get the ongoing stream
	ongoingStream, ok := a.streams[stream.ID]
	if !ok {
		return nil
	}

	return ongoingStream.done
}

// Performs a request to the AceStream backend to start a new stream. It uses the Acexy
// structure to get the host and port of the AceStream backend. The stream is identified
// by the “id“ identifier. Optionally, takes extra parameters to customize the stream.
// Returns the response from the AceStream backend. If the request fails, an error is returned.
// If the `AceStreamMiddleware:error` field is not empty, an error is returned.
func GetStream(a *Acexy, aceId AceID, extraParams url.Values) (*AceStreamMiddleware, error) {
	slog.Debug("Getting stream", "id", aceId, "extraParams", extraParams)
	slog.Debug("Acexy Information", "scheme", a.Scheme, "host", a.Host, "port", a.Port)
	req, err := http.NewRequest("GET", a.Scheme+"://"+a.Host+":"+strconv.Itoa(a.Port)+string(a.Endpoint), nil)
	if err != nil {
		return nil, err
	}

	// Add the query parameters. We use a unique PID to identify the client accessing the stream.
	// This prevents errors when multiple streams are accessed at the same time. Because of
	// using the UUID package, we can be sure that the PID is unique.
	pid := uuid.NewString()
	slog.Debug("Temporary PID", "pid", pid, "stream", aceId)
	if extraParams == nil {
		extraParams = req.URL.Query()
	}
	idType, id := aceId.ID()
	extraParams.Set(string(idType), id)
	extraParams.Set("format", "json")
	extraParams.Set("pid", pid)
	// and set the headers
	req.Header.Set("Content-Type", "application/json")
	req.URL.RawQuery = extraParams.Encode()

	slog.Debug("Request URL", "url", req.URL.String())
	client := &http.Client{
		Timeout: a.NoResponseTimeout,
	}
	res, err := client.Do(req)
	if err != nil {
		slog.Debug("Error getting stream", "error", err)
		return nil, err
	}
	slog.Debug("Stream response", "statusCode", res.StatusCode, "headers", res.Header, "res", res)
	defer res.Body.Close()

	// Read the response into the body
	body, err := io.ReadAll(res.Body)
	if err != nil {
		slog.Debug("Error reading stream response", "error", err)
		return nil, err
	}

	slog.Debug("Stream response", "response", string(body))
	var response AceStreamMiddleware
	if err := json.Unmarshal(body, &response); err != nil {
		slog.Debug("Error unmarshalling stream response", "error", err)
		return nil, err
	}

	if response.Error != "" {
		slog.Debug("Error in stream response", "error", response.Error)
		return nil, errors.New(response.Error)
	}
	return &response, nil
}

// Closes the stream by performing a request to the AceStream backend. The `stream` parameter
// contains the command URL to send data to the middleware. As of the documentation, it is needed
// to add a "method=stop" query parameter to finish the stream.
func CloseStream(stream *AceStream) error {
	req, err := http.NewRequest("GET", stream.CommandURL, nil)
	if err != nil {
		return err
	}

	q := req.URL.Query()
	q.Add("method", "stop")
	req.URL.RawQuery = q.Encode()

	client := &http.Client{
		Timeout: 10 * time.Second, // Reasonable timeout for stop command
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	// Read the response into the body
	body, err := io.ReadAll(res.Body)
	if err != nil {
		slog.Debug("Error reading stream response", "error", err)
		return err
	}

	var response AceStreamCommand
	if err := json.Unmarshal(body, &response); err != nil {
		slog.Debug("Error unmarshalling stream response", "error", err)
		return err
	}

	if response.Error != "" {
		slog.Debug("Error in stream response", "error", response.Error)
		return errors.New(response.Error)
	}
	return nil
}

// Gets the status of a stream. If the `id` parameter is nil, the global status is returned.
// If the stream is not enqueued, an error is returned. The stream is identified by the “id“
// identifier.
func (a *Acexy) GetStatus(id *AceID) (AcexyStatus, error) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	// Return the global status if no ID is given
	if id == nil {
		streams := uint(len(a.streams))
		return AcexyStatus{Streams: &streams}, nil
	}

	// Check if the stream is already enqueued
	if stream, ok := a.streams[*id]; ok {
		return AcexyStatus{
			Clients: &stream.clients,
			ID:      id,
			StatURL: stream.stream.StatURL,
		}, nil
	}

	return AcexyStatus{}, fmt.Errorf(`stream "%s" not found`, id)
}

// ActiveStreamInfo represents information about an active stream
// that can be exposed via the API
type ActiveStreamInfo struct {
	ID                string    `json:"id"`
	PlaybackURL       string    `json:"playback_url"`
	StatURL           string    `json:"stat_url"`
	CommandURL        string    `json:"command_url"`
	Clients           uint      `json:"clients"`
	CreatedAt         time.Time `json:"created_at"`
	HasPlayer         bool      `json:"has_player"`
	EngineHost        string    `json:"engine_host,omitempty"`
	EnginePort        int       `json:"engine_port,omitempty"`
	EngineContainerID string    `json:"engine_container_id,omitempty"`
}

// GetActiveStreams returns information about all currently active streams.
// This is useful for the orchestrator to query which streams are really
// being used, allowing detection and cleanup of hanging streams.
func (a *Acexy) GetActiveStreams() []ActiveStreamInfo {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	activeStreams := make([]ActiveStreamInfo, 0, len(a.streams))
	for aceId, stream := range a.streams {
		activeStreams = append(activeStreams, ActiveStreamInfo{
			ID:                aceId.String(),
			PlaybackURL:       stream.stream.PlaybackURL,
			StatURL:           stream.stream.StatURL,
			CommandURL:        stream.stream.CommandURL,
			Clients:           stream.clients,
			CreatedAt:         stream.createdAt,
			HasPlayer:         stream.player != nil,
			EngineHost:        stream.engineHost,
			EnginePort:        stream.enginePort,
			EngineContainerID: stream.engineContainerID,
		})
	}

	return activeStreams
}

// SetStreamEngineInfo sets the engine information for a stream.
// This is called by the proxy when using orchestrator to track which engine is serving the stream.
func (a *Acexy) SetStreamEngineInfo(aceId AceID, host string, port int, containerID string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	if stream, ok := a.streams[aceId]; ok {
		stream.engineHost = host
		stream.enginePort = port
		stream.engineContainerID = containerID
		slog.Debug("Set engine info for stream", "stream", aceId, "host", host, "port", port, "container_id", containerID)
	} else {
		slog.Warn("Cannot set engine info for non-existent stream", "stream", aceId)
	}
}

// CleanupUnstartedStream removes a stream that was fetched but never started
// This is used when a client disconnects before StartStream is called
func (a *Acexy) CleanupUnstartedStream(aceId AceID) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	stream, ok := a.streams[aceId]
	if !ok {
		return
	}

	// Only clean up if the stream was never started (no clients, no player)
	if stream.clients == 0 && stream.player == nil {
		slog.Info("Cleaning up unstarted stream due to client disconnect", "stream", aceId)
		delete(a.streams, aceId)

		// Close the done channel if not already closed
		select {
		case <-stream.done:
			// Already closed
		default:
			close(stream.done)
		}

		// Try to close the stream on the backend (don't fail if this errors)
		if err := CloseStream(stream.stream); err != nil {
			slog.Debug("Error closing unstarted stream backend", "stream", aceId, "error", err)
		}
	}
}

// Creates a timeout channel that will be closed after the given timeout
func SetTimeout(timeout time.Duration) chan struct{} {
	// Create a channel that will be closed after the given timeout
	timeoutChan := make(chan struct{})

	go func() {
		time.Sleep(timeout)
		close(timeoutChan)
	}()

	return timeoutChan
}
