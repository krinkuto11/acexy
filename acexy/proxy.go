package main

import (
	"flag"
	"io"
	"javinator9889/acexy/lib/acexy"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"
)

var (
	addr          string
	scheme        string
	host          string
	port          int
	streamTimeout time.Duration
	m3u8          bool
)

// The API URL we are listening to
const APIv1_URL = "/ace"

// The transport to be used when connecting to the AceStream middleware. We have to tweak it
// a little bit to avoid compression and to limit the number of connections per host. Otherwise,
// the AceStream Middleware won't work.
var middlewareClient = http.Client{
	Transport: &http.Transport{
		DisableCompression: true,
		MaxIdleConns:       10,
		MaxConnsPerHost:    10,
		IdleConnTimeout:    30 * time.Second,
	},
}

type Proxy struct {
	Acexy *acexy.Acexy
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Verify the request method
	if r.Method != http.MethodGet {
		slog.Error("Method not allowed", "method", r.Method, "path", r.URL.Path)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	// Verify the client has included the ID parameter
	id := q.Get("id")
	if id == "" {
		slog.Error("ID parameter is required", "path", r.URL.Path)
		http.Error(w, "ID parameter is required", http.StatusBadRequest)
		return
	}
	// Remove the ID parameter from the query
	q.Del("id")

	// Verify the client has not included the PID parameter
	if q.Has("pid") {
		slog.Error("PID parameter is not allowed", "path", r.URL.Path)
		http.Error(w, "PID parameter is not allowed", http.StatusBadRequest)
		return
	}

	// Gather the stream information
	stream, err := p.Acexy.StartStream(id, q)
	if err != nil {
		slog.Error("Failed to start stream", "error", err)
		http.Error(w, "Failed to start stream", http.StatusInternalServerError)
		return
	}
	// Defer the stream finish. This will be called when the request is done. When in M3U8 mode,
	// the client connects directly to a subset of endpoints, so we are blind to what the client
	// is doing. However, it periodically polls the M3U8 list to verify nothing has changed,
	// simulating a new connection. Therefore, we can accumulate a lot of open streams and let
	// the timeout finish them.
	//
	// When in MPEG-TS mode, the client connects to the endpoint and waits for the stream to finish.
	// This is a blocking operation, so we can finish the stream when the client disconnects.
	switch p.Acexy.Endpoint {
	case acexy.M3U8_ENDPOINT:
		timedOut := acexy.SetTimeout(streamTimeout)
		defer func() {
			<-timedOut
			p.Acexy.FinishStream(id)
		}()
	case acexy.MPEG_TS_ENDPOINT:
		defer p.Acexy.FinishStream(id)
	}

	slog.Debug("Response", "headers", w.Header())

	resp, err := middlewareClient.Get(stream)
	if err != nil {
		slog.Error("Failed to forward stream", "error", err)
		http.Error(w, "Failed to forward stream", http.StatusInternalServerError)
		return
	}
	slog.Debug("Response", "resp", resp)
	defer resp.Body.Close()

	// Copy the response headers
	for k, v := range resp.Header {
		w.Header().Set(k, v[0])
	}
	if length, err := strconv.Atoi(resp.Header.Get("Content-Length")); err != nil || length == -1 {
		// Set the Transfer-Encoding header to chunked if the Content-Length is not set or is -1
		w.Header().Set("Transfer-Encoding", "chunked")
	}
	w.WriteHeader(resp.StatusCode)
	slog.Debug("Response", "headers", w.Header())

	// Copy the response body until EOF
	if _, err := io.Copy(w, resp.Body); err != nil {
		slog.Error("Failed to copy response body", "error", err)
	}
	slog.Debug("Done", "path", r.URL.Path)
}

func LookupEnvOrString(key string, def string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return def
}

func LookupEnvOrInt(key string, def int) int {
	if val, ok := os.LookupEnv(key); ok {
		i, err := strconv.Atoi(val)
		if err != nil {
			slog.Error("Failed to parse environment variable", "key", key, "value", val)
			return 0
		}
		return i
	}
	return def
}

func LookupEnvOrDuration(key string, def time.Duration) time.Duration {
	if val, ok := os.LookupEnv(key); ok {
		d, err := time.ParseDuration(val)
		if err != nil {
			slog.Error("Failed to parse environment variable", "key", key, "value", val)
			return 0
		}
		return d
	}
	return def
}

func LookupEnvOrBool(key string, def bool) bool {
	if val, ok := os.LookupEnv(key); ok {
		b, err := strconv.ParseBool(val)
		if err != nil {
			slog.Error("Failed to parse environment variable", "key", key, "value", val)
			return false
		}
		return b
	}
	return def
}

func LookupLogLevel() slog.Level {
	if level, ok := os.LookupEnv("ACEXY_LOG_LEVEL"); ok {
		var sl slog.Level

		if err := sl.UnmarshalText([]byte(level)); err != nil {
			slog.Warn("Failed to parse log level", "level", level)
			return slog.LevelInfo
		}
		return sl
	}
	return slog.LevelInfo
}

func parseArgs() {
	// Parse the command-line arguments
	flag.StringVar(
		&addr,
		"addr",
		LookupEnvOrString("ACEXY_LISTEN_ADDR", ":8080"),
		"address to listen on. Can be set with ACEXY_LISTEN_ADDR environment variable.",
	)
	flag.StringVar(
		&scheme,
		"scheme",
		LookupEnvOrString("ACEXY_SCHEME", "http"),
		"scheme to use for the AceStream middleware. Can be set with ACEXY_SCHEME environment variable.",
	)
	flag.StringVar(
		&host,
		"acestream-host",
		LookupEnvOrString("ACEXY_HOST", "localhost"),
		"host to use for the AceStream middleware. Can be set with ACEXY_HOST environment variable.",
	)
	flag.IntVar(
		&port,
		"acestream-port",
		LookupEnvOrInt("ACEXY_PORT", 6878),
		"port to use for the AceStream middleware. Can be set with ACEXY_PORT environment variable.",
	)
	flag.DurationVar(
		&streamTimeout,
		"m3u8-stream-timeout",
		LookupEnvOrDuration("ACEXY_M3U8_STREAM_TIMEOUT", 60*time.Second),
		"timeout in human-readable format to finish the stream. Can be set with ACEXY_M3U8_STREAM_TIMEOUT environment variable.",
	)
	flag.BoolVar(
		&m3u8,
		"m3u8",
		LookupEnvOrBool("ACEXY_M3U8", false),
		"enable M3U8 mode. Can be set with ACEXY_M3U8 environment variable.",
	)
	flag.Parse()
}

func main() {
	// Parse the command-line arguments
	parseArgs()
	slog.SetLogLoggerLevel(LookupLogLevel())
	slog.Debug("CLI Args", "args", flag.CommandLine)

	var endpoint acexy.AcexyEndpoint
	if m3u8 {
		endpoint = acexy.M3U8_ENDPOINT
	} else {
		endpoint = acexy.MPEG_TS_ENDPOINT
	}
	// Create a new Acexy instance
	acexy := &acexy.Acexy{
		Scheme:   scheme,
		Host:     host,
		Port:     port,
		Endpoint: endpoint,
	}
	acexy.Init()

	// Create a new HTTP server
	proxy := &Proxy{Acexy: acexy}
	mux := http.NewServeMux()
	mux.Handle(APIv1_URL+"/getstream", proxy)
	mux.Handle(APIv1_URL+"/getstream/", proxy)
	mux.Handle("/", http.NotFoundHandler())

	// Start the HTTP server
	slog.Info("Starting server", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("Failed to start server", "error", err)
		os.Exit(1)
	}
}
