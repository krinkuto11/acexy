package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProvisionAcestream(t *testing.T) {
	// Test server that simulates the orchestrator
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/provision/acestream":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}

			// Verify the request is properly formatted
			var req aceProvisionRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			// Return a successful acestream provision response
			resp := aceProvisionResponse{
				ContainerID:        "ace123456789",
				HostHTTPPort:       19001,
				ContainerHTTPPort:  40001,
				ContainerHTTPSPort: 45001,
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Create orchestrator client
	client := newOrchClient(server.URL)
	if client == nil {
		t.Fatal("Failed to create orchestrator client")
	}

	// Test provision acestream
	resp, err := client.ProvisionAcestream()
	if err != nil {
		t.Fatalf("ProvisionAcestream failed: %v", err)
	}

	// Verify response
	if resp.ContainerID != "ace123456789" {
		t.Errorf("Expected container ID 'ace123456789', got '%s'", resp.ContainerID)
	}
	if resp.HostHTTPPort != 19001 {
		t.Errorf("Expected host HTTP port 19001, got %d", resp.HostHTTPPort)
	}
	if resp.ContainerHTTPPort != 40001 {
		t.Errorf("Expected container HTTP port 40001, got %d", resp.ContainerHTTPPort)
	}
	if resp.ContainerHTTPSPort != 45001 {
		t.Errorf("Expected container HTTPS port 45001, got %d", resp.ContainerHTTPSPort)
	}
}

func TestSelectBestEngine_WithAvailableEngines(t *testing.T) {
	// Test server that simulates the orchestrator with available engines
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engines":
			// Return some available engines
			engines := []engineState{
				{
					ContainerID: "engine1",
					Host:        "localhost",
					Port:        19001,
					Labels:      map[string]string{"test": "true"},
					FirstSeen:   time.Now(),
					LastSeen:    time.Now(),
				},
				{
					ContainerID: "engine2",
					Host:        "localhost",
					Port:        19002,
					Labels:      map[string]string{"test": "true"},
					FirstSeen:   time.Now(),
					LastSeen:    time.Now(),
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(engines)

		case "/streams":
			// Parse container_id from query params
			containerID := r.URL.Query().Get("container_id")
			status := r.URL.Query().Get("status")

			if status != "started" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			var streams []streamState
			// engine1 has no active streams, engine2 has one active stream
			if containerID == "engine2" {
				streams = []streamState{
					{
						ID:          "stream1",
						KeyType:     "infohash",
						Key:         "test123",
						ContainerID: "engine2",
						Status:      "started",
						StartedAt:   time.Now(),
					},
				}
			}
			// engine1 returns empty streams (available)

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(streams)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Create orchestrator client
	client := newOrchClient(server.URL)
	if client == nil {
		t.Fatal("Failed to create orchestrator client")
	}

	// Test select best engine - should return engine1 (no active streams)
	host, port, err := client.SelectBestEngine()
	if err != nil {
		t.Fatalf("SelectBestEngine failed: %v", err)
	}

	if host != "localhost" {
		t.Errorf("Expected host 'localhost', got '%s'", host)
	}
	if port != 19001 {
		t.Errorf("Expected port 19001, got %d", port)
	}
}

func TestSelectBestEngine_ProvisionNew(t *testing.T) {
	// Test server that simulates the orchestrator with no available engines
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engines":
			// Return engines but all busy
			engines := []engineState{
				{
					ContainerID: "engine1",
					Host:        "localhost",
					Port:        19001,
					Labels:      map[string]string{"test": "true"},
					FirstSeen:   time.Now(),
					LastSeen:    time.Now(),
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(engines)

		case "/streams":
			// Return active stream for engine1
			streams := []streamState{
				{
					ID:          "stream1",
					KeyType:     "infohash",
					Key:         "test123",
					ContainerID: "engine1",
					Status:      "started",
					StartedAt:   time.Now(),
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(streams)

		case "/provision/acestream":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}

			// Verify we're calling acestream-specific endpoint
			var req aceProvisionRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			// Return a successful acestream provision response
			resp := aceProvisionResponse{
				ContainerID:        "new_ace123",
				HostHTTPPort:       19003,
				ContainerHTTPPort:  40003,
				ContainerHTTPSPort: 45003,
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Create orchestrator client
	client := newOrchClient(server.URL)
	if client == nil {
		t.Fatal("Failed to create orchestrator client")
	}

	// Test select best engine - should provision new one
	host, port, err := client.SelectBestEngine()
	if err != nil {
		t.Fatalf("SelectBestEngine failed: %v", err)
	}

	if host != "localhost" {
		t.Errorf("Expected host 'localhost', got '%s'", host)
	}
	if port != 19003 {
		t.Errorf("Expected port 19003, got %d", port)
	}
}

func TestSelectBestEngine_MultipleEnginesUniquePorts(t *testing.T) {
	// Test that multiple engines get unique ports
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engines":
			// Return no engines initially
			engines := []engineState{}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(engines)

		case "/provision/acestream":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}

			requestCount++
			
			// Return different ports for each request
			resp := aceProvisionResponse{
				ContainerID:        fmt.Sprintf("ace_%d", requestCount),
				HostHTTPPort:       19000 + requestCount,
				ContainerHTTPPort:  40000 + requestCount,
				ContainerHTTPSPort: 45000 + requestCount,
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Create orchestrator client
	client := newOrchClient(server.URL)
	if client == nil {
		t.Fatal("Failed to create orchestrator client")
	}

	// Test provisioning multiple engines
	usedPorts := make(map[int]bool)
	numEngines := 3

	for i := 0; i < numEngines; i++ {
		host, port, err := client.SelectBestEngine()
		if err != nil {
			t.Fatalf("SelectBestEngine failed on iteration %d: %v", i, err)
		}

		if host != "localhost" {
			t.Errorf("Expected host 'localhost', got '%s'", host)
		}

		// Verify port is unique
		if usedPorts[port] {
			t.Errorf("Port %d was reused! All ports should be unique", port)
		}
		usedPorts[port] = true

		// Verify port is in expected range
		expectedPort := 19001 + i
		if port != expectedPort {
			t.Errorf("Expected port %d, got %d", expectedPort, port)
		}
	}

	if len(usedPorts) != numEngines {
		t.Errorf("Expected %d unique ports, got %d", numEngines, len(usedPorts))
	}
}

func TestOrchestrator_CallingCorrectEndpoint(t *testing.T) {
	// Test that acexy calls the correct acestream-specific endpoint
	var calledPaths []string
	
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calledPaths = append(calledPaths, r.URL.Path)
		
		switch r.URL.Path {
		case "/provision/acestream":
			// This is the correct endpoint
			resp := aceProvisionResponse{
				ContainerID:        "ace123",
				HostHTTPPort:       19001,
				ContainerHTTPPort:  40001,
				ContainerHTTPSPort: 45001,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			
		case "/provision":
			// This would be the generic endpoint (should not be called)
			t.Error("acexy called generic /provision endpoint instead of acestream-specific /provision/acestream")
			w.WriteHeader(http.StatusBadRequest)
			
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Create orchestrator client
	client := newOrchClient(server.URL)
	if client == nil {
		t.Fatal("Failed to create orchestrator client")
	}

	// Call provision
	_, err := client.ProvisionAcestream()
	if err != nil {
		t.Fatalf("ProvisionAcestream failed: %v", err)
	}

	// Verify the correct endpoint was called
	found := false
	for _, path := range calledPaths {
		if path == "/provision/acestream" {
			found = true
			break
		}
	}
	
	if !found {
		t.Errorf("Expected /provision/acestream to be called, but paths called were: %v", calledPaths)
	}
	
	// Verify generic endpoint was not called
	for _, path := range calledPaths {
		if path == "/provision" {
			t.Error("Generic /provision endpoint was called - acexy should use acestream-specific endpoint")
		}
	}
}

func TestOrchestrator_RequestFormat(t *testing.T) {
	// Test that the request sent to orchestrator has the correct format
	var receivedRequest aceProvisionRequest
	
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/provision/acestream" {
			// Capture the request
			if err := json.NewDecoder(r.Body).Decode(&receivedRequest); err != nil {
				t.Errorf("Failed to decode request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			
			// Verify Content-Type header
			contentType := r.Header.Get("Content-Type")
			if contentType != "application/json" {
				t.Errorf("Expected Content-Type 'application/json', got '%s'", contentType)
			}

			resp := aceProvisionResponse{
				ContainerID:        "ace123",
				HostHTTPPort:       19001,
				ContainerHTTPPort:  40001,
				ContainerHTTPSPort: 45001,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	// Create orchestrator client
	client := newOrchClient(server.URL)
	client.key = "test-api-key"

	// Call provision
	_, err := client.ProvisionAcestream()
	if err != nil {
		t.Fatalf("ProvisionAcestream failed: %v", err)
	}

	// Verify request format
	if receivedRequest.Labels == nil {
		t.Error("Expected Labels field to be initialized (not nil)")
	}
	if receivedRequest.Env == nil {
		t.Error("Expected Env field to be initialized (not nil)")
	}
}