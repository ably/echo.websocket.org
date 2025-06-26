package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFrontendTimeoutHandling(t *testing.T) {
	handler := http.HandlerFunc(handler)
	server := httptest.NewServer(handler)
	defer server.Close()

	// Get the frontend HTML
	resp, err := http.Get(server.URL + "/.ws")
	if err != nil {
		t.Fatalf("Failed to get frontend: %v", err)
	}
	defer resp.Body.Close()

	// Read the response body
	buf := make([]byte, 1024*1024) // 1MB buffer
	n, _ := resp.Body.Read(buf)
	html := string(buf[:n])

	// Check that the frontend has timeout detection logic
	tests := []struct {
		name     string
		contains string
	}{
		{
			name:     "Has timeout message detection",
			contains: "Connection timeout",
		},
		{
			name:     "Sets autoReconnect to false on timeout",
			contains: "autoReconnect = false",
		},
		{
			name:     "Has lastMessageWasTimeout flag",
			contains: "lastMessageWasTimeout",
		},
		{
			name:     "Logs timeout prominently",
			contains: "SERVER TIMEOUT",
		},
		{
			name:     "Shows no auto-reconnect message",
			contains: "no auto-reconnect",
		},
		{
			name:     "Handles timeout in close event",
			contains: "isTimeoutClose || lastMessageWasTimeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(html, tt.contains) {
				t.Errorf("Frontend HTML does not contain expected string: %s", tt.contains)
			}
		})
	}
}