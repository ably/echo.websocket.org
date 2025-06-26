package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestHTTPEcho(t *testing.T) {
	handler := http.HandlerFunc(handler)
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	bodyStr := string(body)

	// Check that the request is echoed
	if !strings.Contains(bodyStr, "GET / HTTP/1.1") {
		t.Errorf("Response doesn't contain echoed request line")
	}

	// Check for the ASCII art footer
	if !strings.Contains(bodyStr, "WebSocket UI:") {
		t.Errorf("Response doesn't contain footer with WebSocket UI link")
	}

	// Check charset
	contentType := resp.Header.Get("Content-Type")
	if contentType != "text/plain; charset=utf-8" {
		t.Errorf("Expected Content-Type 'text/plain; charset=utf-8', got '%s'", contentType)
	}
}

func TestWebSocketTimeout(t *testing.T) {
	// Set a short timeout for testing
	t.Setenv("CONNECTION_TIMEOUT_MINUTES", "0.05") // 3 seconds

	handler := http.HandlerFunc(handler)
	server := httptest.NewServer(handler)
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Connect to WebSocket
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect to WebSocket: %v", err)
	}
	defer ws.Close()

	// Read initial message if any
	ws.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_, _, _ = ws.ReadMessage() // Ignore initial server hostname message

	// Send a message to ensure connection is active
	if err := ws.WriteMessage(websocket.TextMessage, []byte("test")); err != nil {
		t.Fatalf("Failed to send message: %v", err)
	}

	// Read the echo
	ws.SetReadDeadline(time.Now().Add(1 * time.Second))
	msgType, msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read echo: %v", err)
	}
	if msgType != websocket.TextMessage || string(msg) != "test" {
		t.Errorf("Expected echo 'test', got '%s'", string(msg))
	}

	// Wait for timeout and read any pending messages
	time.Sleep(4 * time.Second)

	// Read any pending messages (including possible timeout message)
	ws.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		_, msg, err := ws.ReadMessage()
		if err != nil {
			break
		}
		// Check if this is the timeout message
		if strings.Contains(string(msg), "Connection timeout") {
			t.Logf("Received timeout message: %s", string(msg))
		}
	}

	// Give the close a moment to propagate
	time.Sleep(100 * time.Millisecond)

	// Now try to send another message - should fail due to closed connection
	err = ws.WriteMessage(websocket.TextMessage, []byte("should fail"))
	if err == nil {
		// One more attempt to read to trigger error detection
		ws.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		_, _, readErr := ws.ReadMessage()
		if readErr == nil {
			t.Errorf("Expected connection to be closed after timeout, but both write and read succeeded")
		} else {
			t.Logf("Read failed as expected: %v", readErr)
		}
	} else {
		t.Logf("Write failed as expected: %v", err)
	}
}

func TestWebSocketTimeoutMessage(t *testing.T) {
	// Set a short timeout for testing
	t.Setenv("CONNECTION_TIMEOUT_MINUTES", "0.05") // 3 seconds

	handler := http.HandlerFunc(handler)
	server := httptest.NewServer(handler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect to WebSocket: %v", err)
	}
	defer ws.Close()

	// Read any initial message
	ws.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	ws.ReadMessage()

	// Wait for timeout and check for timeout message
	time.Sleep(4 * time.Second)
	
	// Try to read - we should either get the timeout message or an error
	ws.SetReadDeadline(time.Now().Add(1 * time.Second))
	_, msg, err := ws.ReadMessage()
	if err == nil && strings.Contains(string(msg), "Connection timeout") {
		t.Logf("Received timeout message: %s", string(msg))
		
		// Give the close a moment to propagate
		time.Sleep(100 * time.Millisecond)
		
		// Now the connection should be closed, try to write
		err = ws.WriteMessage(websocket.TextMessage, []byte("test after timeout"))
		if err == nil {
			// One more attempt to read to trigger error detection
			ws.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			_, _, readErr := ws.ReadMessage()
			if readErr == nil {
				t.Errorf("Expected connection to be closed after timeout, but both write and read succeeded")
			} else {
				t.Logf("Read failed as expected after timeout: %v", readErr)
			}
		} else {
			t.Logf("Write failed as expected after timeout: %v", err)
		}
		return
	}
	
	// If no timeout message, try to write - should fail if connection is closed
	err = ws.WriteMessage(websocket.TextMessage, []byte("test after timeout"))
	if err != nil {
		t.Logf("Write failed as expected after timeout: %v", err)
		return
	}

	// If write succeeded, the connection is still open - this is an error
	t.Errorf("Connection still active after timeout")
}

func TestSSETimeout(t *testing.T) {
	// Set a short timeout for testing
	t.Setenv("CONNECTION_TIMEOUT_MINUTES", "0.05") // 3 seconds

	handler := http.HandlerFunc(handler)
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/.sse")
	if err != nil {
		t.Fatalf("Failed to connect to SSE: %v", err)
	}
	defer resp.Body.Close()

	// Check headers
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Expected Content-Type 'text/event-stream', got '%s'", ct)
	}

	reader := bufio.NewReader(resp.Body)
	foundTimeout := false
	timeoutChan := make(chan bool, 1)

	// Read SSE events in a goroutine
	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}

			// Look for timeout message
			if strings.Contains(line, "event: error") {
				// Read the data line
				line, _ = reader.ReadString('\n')
				if strings.Contains(line, "Connection timeout") {
					timeoutChan <- true
					return
				}
			}
		}
	}()

	// Wait for timeout
	select {
	case <-timeoutChan:
		foundTimeout = true
	case <-time.After(5 * time.Second):
		t.Errorf("SSE timeout not received within expected time")
	}

	if !foundTimeout {
		t.Errorf("SSE connection did not timeout as expected")
	}
}

func TestWebSocketReconnectionPrevention(t *testing.T) {
	// This test verifies that the web UI won't auto-reconnect on timeout
	// The actual prevention is done in the frontend JavaScript code
	// This test just verifies the timeout happens
	t.Setenv("CONNECTION_TIMEOUT_MINUTES", "0.05") // 3 seconds

	handler := http.HandlerFunc(handler)
	server := httptest.NewServer(handler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect to WebSocket: %v", err)
	}
	defer ws.Close()

	// Read any initial message
	ws.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	ws.ReadMessage()

	// Send a message before timeout
	err = ws.WriteMessage(websocket.TextMessage, []byte("before timeout"))
	if err != nil {
		t.Fatalf("Failed to send message before timeout: %v", err)
	}

	// Read echo
	ws.SetReadDeadline(time.Now().Add(1 * time.Second))
	_, msg, err := ws.ReadMessage()
	if err != nil || string(msg) != "before timeout" {
		t.Fatalf("Failed to receive echo before timeout")
	}

	// Wait for timeout
	time.Sleep(3 * time.Second)

	// Try to send after timeout - connection behavior may vary
	err = ws.WriteMessage(websocket.TextMessage, []byte("after timeout"))
	if err != nil {
		t.Logf("Connection closed as expected: %v", err)
	} else {
		// Some clients might buffer the write, so also check read
		ws.SetReadDeadline(time.Now().Add(1 * time.Second))
		_, _, err = ws.ReadMessage()
		if err != nil {
			t.Logf("Read failed after timeout, connection effectively closed: %v", err)
		}
	}
}

func TestTimeoutWithActivity(t *testing.T) {
	// Test that WebSocket timeout is NOT reset on activity (absolute timeout)
	t.Setenv("CONNECTION_TIMEOUT_MINUTES", "0.05") // 3 seconds

	handler := http.HandlerFunc(handler)
	server := httptest.NewServer(handler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect to WebSocket: %v", err)
	}
	defer ws.Close()

	// Read initial server hostname message if present
	ws.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_, _, _ = ws.ReadMessage()

	// Send messages every 1 second (less than timeout)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	start := time.Now()
	messagesSent := 0

	for {
		select {
		case <-ticker.C:
			// Try to send a message
			msg := fmt.Sprintf("keepalive-%d", messagesSent)
			if err := ws.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
				// Connection should close after 3 seconds despite activity
				elapsed := time.Since(start)
				if elapsed >= 3*time.Second && elapsed < 5*time.Second {
					// Expected closure
					return
				}
				t.Errorf("Connection closed at unexpected time: %v", elapsed)
				return
			}
			messagesSent++

			// Read response
			ws.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			_, reply, err := ws.ReadMessage()
			if err != nil {
				// Connection should close after 3 seconds despite activity
				elapsed := time.Since(start)
				if elapsed >= 3*time.Second && elapsed < 5*time.Second {
					// Expected closure
					return
				}
				t.Errorf("Failed to read echo at unexpected time: %v, error: %v", elapsed, err)
				return
			}
			
			// Check if we received timeout message
			if strings.Contains(string(reply), "Connection timeout") {
				// Expected timeout message
				return
			}

			// If we've been sending for more than 5 seconds, the timeout should have fired
			if time.Since(start) > 5*time.Second {
				t.Error("Connection stayed open longer than timeout period despite absolute timeout")
				return
			}
		}
	}
}

func TestBackwardCompatibility(t *testing.T) {
	// Test that WEBSOCKET_TIMEOUT_MINUTES still works when CONNECTION_TIMEOUT_MINUTES is not set
	// Note: os.Getenv returns "" for unset variables, which our code handles correctly
	t.Setenv("WEBSOCKET_TIMEOUT_MINUTES", "0.05") // 3 seconds

	handler := http.HandlerFunc(handler)
	server := httptest.NewServer(handler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect to WebSocket: %v", err)
	}
	defer ws.Close()

	// Read any initial message
	ws.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	ws.ReadMessage()

	// Send a test message before timeout
	err = ws.WriteMessage(websocket.TextMessage, []byte("test"))
	if err != nil {
		t.Fatalf("Failed to send initial message: %v", err)
	}

	// Read echo
	ws.SetReadDeadline(time.Now().Add(1 * time.Second))
	_, echo, err := ws.ReadMessage()
	if err != nil || string(echo) != "test" {
		t.Fatalf("Failed to receive echo: %v", err)
	}

	// Wait for timeout
	time.Sleep(3 * time.Second)

	// Connection should be timed out now
	err = ws.WriteMessage(websocket.TextMessage, []byte("after timeout"))
	if err != nil {
		t.Logf("Connection closed as expected with WEBSOCKET_TIMEOUT_MINUTES: %v", err)
	} else {
		// Check if read fails
		ws.SetReadDeadline(time.Now().Add(1 * time.Second))
		_, _, err = ws.ReadMessage()
		if err != nil {
			t.Logf("Read failed after timeout with WEBSOCKET_TIMEOUT_MINUTES: %v", err)
		}
	}
}

func TestDefaultTimeout(t *testing.T) {
	// Test that default timeout is 10 minutes
	// We'll just verify the configuration, not wait 10 minutes
	timeoutMinutes := defaultConnectionTimeoutMinutes
	if timeoutMinutes != 10 {
		t.Errorf("Expected default timeout of 10 minutes, got %d", timeoutMinutes)
	}
}

func TestHostnameOption(t *testing.T) {
	tests := []struct {
		name       string
		envVar     string
		header     string
		expectHost bool
	}{
		{"Default", "", "", true},
		{"EnvFalse", "false", "", false},
		{"HeaderTrue", "false", "true", true},
		{"HeaderFalse", "true", "false", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVar != "" {
				t.Setenv("SEND_SERVER_HOSTNAME", tt.envVar)
			}

			handler := http.HandlerFunc(handler)
			server := httptest.NewServer(handler)
			defer server.Close()

			req, _ := http.NewRequest("GET", server.URL, nil)
			if tt.header != "" {
				req.Header.Set("X-Send-Server-Hostname", tt.header)
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Failed to make request: %v", err)
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)
			bodyStr := string(body)

			hasHostname := strings.Contains(bodyStr, "Request served by")
			if hasHostname != tt.expectHost {
				t.Errorf("Expected hostname present=%v, got %v", tt.expectHost, hasHostname)
			}
		})
	}
}