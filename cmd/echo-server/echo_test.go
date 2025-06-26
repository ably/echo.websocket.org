package main

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestWebSocketBasicEcho tests the core WebSocket echo functionality
func TestWebSocketBasicEcho(t *testing.T) {
	handler := http.HandlerFunc(handler)
	server := httptest.NewServer(handler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect to WebSocket: %v", err)
	}
	defer ws.Close()

	// Skip initial server hostname message if present
	ws.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	ws.ReadMessage()

	tests := []struct {
		name    string
		message string
		msgType int
	}{
		{"Simple text", "Hello, WebSocket!", websocket.TextMessage},
		{"JSON data", `{"type":"test","value":123}`, websocket.TextMessage},
		{"Empty message", "", websocket.TextMessage},
		{"Special chars", "Hello 👋 World! @#$%^&*()", websocket.TextMessage},
		{"Multiline", "Line 1\nLine 2\nLine 3", websocket.TextMessage},
		{"Binary data", "Binary\x00\x01\x02\x03", websocket.BinaryMessage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Send message
			err := ws.WriteMessage(tt.msgType, []byte(tt.message))
			if err != nil {
				t.Fatalf("Failed to send message: %v", err)
			}

			// Read echo
			ws.SetReadDeadline(time.Now().Add(2 * time.Second))
			msgType, msg, err := ws.ReadMessage()
			if err != nil {
				t.Fatalf("Failed to read echo: %v", err)
			}

			// Verify message type
			if msgType != tt.msgType {
				t.Errorf("Expected message type %d, got %d", tt.msgType, msgType)
			}

			// Verify content
			if string(msg) != tt.message {
				t.Errorf("Expected echo '%s', got '%s'", tt.message, string(msg))
			}
		})
	}
}

// TestWebSocketMultipleClients tests echo with multiple concurrent clients
func TestWebSocketMultipleClients(t *testing.T) {
	handler := http.HandlerFunc(handler)
	server := httptest.NewServer(handler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Create multiple clients
	numClients := 5
	clients := make([]*websocket.Conn, numClients)

	for i := 0; i < numClients; i++ {
		ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("Failed to connect client %d: %v", i, err)
		}
		defer ws.Close()
		clients[i] = ws

		// Skip initial message
		ws.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		ws.ReadMessage()
	}

	// Each client sends a unique message
	for i, ws := range clients {
		msg := string(rune('A' + i)) + " says hello"
		err := ws.WriteMessage(websocket.TextMessage, []byte(msg))
		if err != nil {
			t.Fatalf("Client %d failed to send: %v", i, err)
		}
	}

	// Each client should receive only its own echo
	for i, ws := range clients {
		expectedMsg := string(rune('A' + i)) + " says hello"
		
		ws.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, msg, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("Client %d failed to read echo: %v", i, err)
		}

		if string(msg) != expectedMsg {
			t.Errorf("Client %d expected '%s', got '%s'", i, expectedMsg, string(msg))
		}
	}
}

// TestSSEBasicStream tests the core SSE functionality
func TestSSEBasicStream(t *testing.T) {
	handler := http.HandlerFunc(handler)
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/.sse")
	if err != nil {
		t.Fatalf("Failed to connect to SSE: %v", err)
	}
	defer resp.Body.Close()

	// Verify headers
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Expected Content-Type 'text/event-stream', got '%s'", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Expected Cache-Control 'no-cache', got '%s'", cc)
	}

	reader := bufio.NewReader(resp.Body)
	events := make(map[string]int)
	timeData := []string{}
	
	// Read events for 3 seconds
	start := time.Now()
	for time.Since(start) < 3*time.Second {
		// Set a deadline for the read operation
		line, err := reader.ReadString('\n')
		if err != nil {
			continue
		}

		line = strings.TrimSpace(line)
		
		if strings.HasPrefix(line, "event: ") {
			eventType := strings.TrimPrefix(line, "event: ")
			events[eventType]++
		} else if strings.HasPrefix(line, "data: ") && strings.Contains(line, "T") && strings.Contains(line, ":") {
			// This looks like a timestamp
			data := strings.TrimPrefix(line, "data: ")
			if _, err := time.Parse(time.RFC3339, data); err == nil {
				timeData = append(timeData, data)
			}
		}
	}

	// Verify we got expected event types
	if events["server"] == 0 {
		t.Log("Note: 'server' event may not appear if SEND_SERVER_HOSTNAME is false")
	}

	if events["request"] == 0 {
		t.Error("Did not receive 'request' event")
	}

	if events["time"] < 2 {
		t.Errorf("Expected at least 2 'time' events, got %d", events["time"])
	}

	// Verify we got valid timestamps
	if len(timeData) < 2 {
		t.Errorf("Expected at least 2 time data points, got %d", len(timeData))
	}

	t.Logf("Received events: %v", events)
	t.Logf("Received %d valid timestamps", len(timeData))
}

// TestSSEMultipleClients tests SSE with multiple concurrent clients
func TestSSEMultipleClients(t *testing.T) {
	handler := http.HandlerFunc(handler)
	server := httptest.NewServer(handler)
	defer server.Close()

	numClients := 3
	clients := make([]*http.Response, numClients)

	// Connect multiple SSE clients
	for i := 0; i < numClients; i++ {
		resp, err := http.Get(server.URL + "/.sse")
		if err != nil {
			t.Fatalf("Failed to connect SSE client %d: %v", i, err)
		}
		defer resp.Body.Close()
		clients[i] = resp
	}

	// Each client should receive time events
	for i, resp := range clients {
		reader := bufio.NewReader(resp.Body)
		foundTime := false

		// Read for up to 2 seconds
		done := make(chan bool)
		go func() {
			time.Sleep(2 * time.Second)
			done <- true
		}()

		readLoop:
		for {
			select {
			case <-done:
				break readLoop
			default:
				line, err := reader.ReadString('\n')
				if err != nil {
					continue
				}
				if strings.Contains(line, "event: time") {
					foundTime = true
					break readLoop
				}
			}
		}

		if !foundTime {
			t.Errorf("SSE client %d did not receive time event", i)
		}
	}
}

// TestWebSocketRapidMessages tests handling of rapid message sending
func TestWebSocketRapidMessages(t *testing.T) {
	handler := http.HandlerFunc(handler)
	server := httptest.NewServer(handler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect to WebSocket: %v", err)
	}
	defer ws.Close()

	// Skip initial message
	ws.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	ws.ReadMessage()

	// Send multiple messages rapidly
	numMessages := 20
	for i := 0; i < numMessages; i++ {
		msg := string(rune('A' + (i % 26)))
		err := ws.WriteMessage(websocket.TextMessage, []byte(msg))
		if err != nil {
			t.Fatalf("Failed to send message %d: %v", i, err)
		}
	}

	// Read all echoes
	received := 0
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for i := 0; i < numMessages; i++ {
		expectedMsg := string(rune('A' + (i % 26)))
		_, msg, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("Failed to read echo %d: %v", i, err)
		}
		if string(msg) == expectedMsg {
			received++
		}
	}

	if received != numMessages {
		t.Errorf("Expected %d echoes, received %d", numMessages, received)
	}
}

// TestWebSocketLargeMessage tests handling of large messages
func TestWebSocketLargeMessage(t *testing.T) {
	handler := http.HandlerFunc(handler)
	server := httptest.NewServer(handler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect to WebSocket: %v", err)
	}
	defer ws.Close()

	// Skip initial message
	ws.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	ws.ReadMessage()

	// Create a large message (1MB)
	largeMsg := strings.Repeat("Hello WebSocket! ", 65536) // ~1MB

	// Send large message
	err = ws.WriteMessage(websocket.TextMessage, []byte(largeMsg))
	if err != nil {
		t.Fatalf("Failed to send large message: %v", err)
	}

	// Read echo
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, echo, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read large echo: %v", err)
	}

	if string(echo) != largeMsg {
		t.Errorf("Large message echo mismatch: got %d bytes, expected %d bytes", 
			len(echo), len(largeMsg))
	}
}

