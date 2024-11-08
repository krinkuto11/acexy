package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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

// The stream information is stored in a structure referencing the `AceStreamResponse`
// plus some extra information to determine whether we should keep the stream alive or not.
type AceStream struct {
	PlaybackURL string
	StatURL     string
	CommandURL  string

	clients uint
}

// Structure referencing the AceStream Proxy - this is, ourselves
type Acexy struct {
	Scheme   string
	Host     string
	Port     int
	Endpoint AcexyEndpoint

	// Information about ongoing streams
	streams map[string]AceStream
	mutex   *sync.Mutex
}

type AcexyEndpoint string

// The AceStream API available endpoints
const (
	M3U8_ENDPOINT    AcexyEndpoint = "/ace/manifest.m3u8"
	MPEG_TS_ENDPOINT AcexyEndpoint = "/ace/getstream"
)

// Initializes the Acexy structure
func (a *Acexy) Init() {
	a.streams = make(map[string]AceStream)
	a.mutex = &sync.Mutex{}
}

// Starts a new stream. The stream is enqueued in the AceStream backend, returning a playback
// URL to reproduce it and a command URL to finish it. If the stream is already enqueued,
// the playback URL is returned. A number of clients can be reproducing the same stream at
// the same time through the middleware. When the last client finishes, the stream is removed.
// The stream is identified by the “id“ identifier. Optionally, takes extra parameters to
// customize the stream.
func (a *Acexy) StartStream(id string, extraParams url.Values) (string, error) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	// Check if the stream is already enqueued
	if stream, ok := a.streams[id]; ok {
		// Register the new client
		stream.clients++
		a.streams[id] = stream
		slog.Info("Reusing existing", "stream", id, "clients", stream.clients)
		return stream.PlaybackURL, nil
	}

	// Enqueue the middleware
	middleware, err := GetStream(a, id, extraParams)
	if err != nil {
		slog.Error("Error getting stream middleware", "error", err)
		return "", err
	}

	// We got the stream information, build the structure around it and register the stream
	slog.Debug("Middleware Information", "id", id, "middleware", middleware)
	stream := AceStream{
		PlaybackURL: middleware.Response.PlaybackURL,
		StatURL:     middleware.Response.StatURL,
		CommandURL:  middleware.Response.CommandURL,
		clients:     1,
	}

	a.streams[id] = stream
	slog.Info("Started new stream", "id", id, "clients", stream.clients)
	return stream.PlaybackURL, nil
}

// Finishes a stream. The stream is removed from the AceStream backend. If the stream is not
// enqueued, an error is returned. If the stream has clients reproducing it, the stream is not
// removed. The stream is identified by the “id“ identifier.
func (a *Acexy) FinishStream(id string) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	// Check if the stream is already enqueued
	if stream, ok := a.streams[id]; ok {
		// Unregister the client
		stream.clients--
		a.streams[id] = stream

		slog.Info("Finishing", "stream", id, "clients", stream.clients)
		if stream.clients == 0 {
			// Always remove the stream from the map, even if an error occurs. AceStream
			// will remove the stream if it is not used.
			defer delete(a.streams, id)

			// Close the stream
			if err := CloseStream(&stream); err != nil {
				slog.Error("Error closing stream", "error", err)
				return err
			}
			slog.Info("Stream done", "stream", id)
		}
		return nil
	}

	slog.Error("Stream not found", "stream", id)
	return fmt.Errorf(`stream "%s" not found`, id)
}

// Performs a request to the AceStream backend to start a new stream. It uses the Acexy
// structure to get the host and port of the AceStream backend. The stream is identified
// by the “id“ identifier. Optionally, takes extra parameters to customize the stream.
// Returns the response from the AceStream backend. If the request fails, an error is returned.
// If the `AceStreamMiddleware:error` field is not empty, an error is returned.
func GetStream(a *Acexy, id string, extraParams url.Values) (*AceStreamMiddleware, error) {
	slog.Debug("Getting stream", "id", id, "extraParams", extraParams)
	slog.Debug("Acexy Information", "scheme", a.Scheme, "host", a.Host, "port", a.Port)
	req, err := http.NewRequest("GET", a.Scheme+"://"+a.Host+":"+strconv.Itoa(a.Port)+string(a.Endpoint), nil)
	if err != nil {
		return nil, err
	}

	// Add the query parameters. We use a unique PID to identify the client accessing the stream.
	// This prevents errors when multiple streams are accessed at the same time. Because of
	// using the UUID package, we can be sure that the PID is unique.
	pid := uuid.NewString()
	slog.Info("Temporary PID", "pid", pid, "stream", id)
	if extraParams == nil {
		extraParams = req.URL.Query()
	}
	extraParams.Add("id", id)
	extraParams.Add("format", "json")
	extraParams.Add("pid", pid)
	// and set the headers
	req.Header.Set("Content-Type", "application/json")
	req.URL.RawQuery = extraParams.Encode()

	slog.Debug("Request URL", "url", req.URL.String())
	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		slog.Warn("Error getting stream", "error", err)
		return nil, err
	}
	defer res.Body.Close()

	// Read the response into the body
	body, err := io.ReadAll(res.Body)
	if err != nil {
		slog.Warn("Error reading stream response", "error", err)
		return nil, err
	}

	slog.Debug("Stream response", "response", string(body))
	var response AceStreamMiddleware
	if err := json.Unmarshal(body, &response); err != nil {
		slog.Warn("Error unmarshalling stream response", "error", err)
		return nil, err
	}

	if response.Error != "" {
		slog.Warn("Error in stream response", "error", response.Error)
		return nil, err
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

	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	// Read the response into the body
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	var response AceStreamCommand
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}

	if response.Error != "" {
		return err
	}
	return nil
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
