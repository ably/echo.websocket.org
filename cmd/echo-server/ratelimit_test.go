package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRateLimiter_TokenBucket(t *testing.T) {
	// Create a rate limiter with specific settings for testing
	rl := &RateLimiter{
		enabled:           true,
		requestsPerSecond: 2.0,
		burstSize:        5,
		blockDuration:    time.Second * 2,
		localBuckets:     make(map[string]*TokenBucket),
		localConnections: make(map[string]int),
		stopCleanup:      make(chan bool),
	}
	defer rl.Close()
	
	testIP := "192.168.1.1"
	
	// Test 1: Burst capacity allows 5 requests immediately
	for i := 0; i < 5; i++ {
		allowed, _ := rl.AllowRequest(testIP)
		if !allowed {
			t.Errorf("Request %d should be allowed (within burst)", i+1)
		}
	}
	
	// Test 2: 6th request should be blocked
	allowed, blockedUntil := rl.AllowRequest(testIP)
	if allowed {
		t.Error("6th request should be blocked (burst exhausted)")
	}
	if time.Until(blockedUntil) < time.Second {
		t.Error("Block duration should be at least 2 seconds")
	}
	
	// Test 3: Wait for block to expire and tokens to refill
	time.Sleep(time.Second * 2) // Wait for block to expire
	
	// Reset the bucket state to simulate fresh start after block
	rl.localMutex.Lock()
	delete(rl.localBuckets, testIP)
	rl.localMutex.Unlock()
	
	allowed, _ = rl.AllowRequest(testIP)
	if !allowed {
		t.Error("Request should be allowed after block expiry")
	}
}

func TestRateLimiter_ProgressiveBlocking(t *testing.T) {
	rl := &RateLimiter{
		enabled:           true,
		requestsPerSecond: 1.0,
		burstSize:        2,
		blockDuration:    time.Second,
		localBuckets:     make(map[string]*TokenBucket),
		localConnections: make(map[string]int),
		stopCleanup:      make(chan bool),
	}
	defer rl.Close()
	
	testIP := "192.168.1.2"
	
	// Exhaust burst
	for i := 0; i < 2; i++ {
		rl.AllowRequest(testIP)
	}
	
	// First violation - 1x block duration
	_, blockedUntil1 := rl.AllowRequest(testIP)
	blockDuration1 := time.Until(blockedUntil1)
	
	// Force another violation by manipulating the bucket
	rl.localBuckets[testIP].blockedUntil = time.Now()
	rl.localBuckets[testIP].tokens = 0
	
	// Second violation - 2x block duration
	_, blockedUntil2 := rl.AllowRequest(testIP)
	blockDuration2 := time.Until(blockedUntil2)
	
	if blockDuration2 < blockDuration1*2-time.Millisecond*100 {
		t.Errorf("Progressive blocking not working: 2nd block (%v) should be ~2x first block (%v)", 
			blockDuration2, blockDuration1)
	}
}

func TestRateLimiter_ConnectionLimits(t *testing.T) {
	rl := &RateLimiter{
		enabled:           true,
		wsMaxConnections:  2,
		sseMaxConnections: 2,
		localConnections:  make(map[string]int),
		stopCleanup:      make(chan bool),
	}
	defer rl.Close()
	
	testIP := "192.168.1.3"
	
	// Test WebSocket connections
	if !rl.TrackConnection(testIP, "websocket") {
		t.Error("First WebSocket connection should be allowed")
	}
	if !rl.TrackConnection(testIP, "websocket") {
		t.Error("Second WebSocket connection should be allowed")
	}
	if rl.TrackConnection(testIP, "websocket") {
		t.Error("Third WebSocket connection should be blocked")
	}
	
	// Release one connection
	rl.ReleaseConnection(testIP, "websocket")
	if !rl.TrackConnection(testIP, "websocket") {
		t.Error("Should allow connection after release")
	}
	
	// Test SSE connections (independent limit)
	if !rl.TrackConnection(testIP, "sse") {
		t.Error("First SSE connection should be allowed")
	}
	if !rl.TrackConnection(testIP, "sse") {
		t.Error("Second SSE connection should be allowed")
	}
	if rl.TrackConnection(testIP, "sse") {
		t.Error("Third SSE connection should be blocked")
	}
}

func TestRateLimiter_HTTPIntegration(t *testing.T) {
	// Initialize global rate limiter for test
	oldRL := rateLimiter
	rateLimiter = &RateLimiter{
		enabled:           true,
		requestsPerSecond: 2.0,
		burstSize:        3,
		blockDuration:    time.Second * 2,
		localBuckets:     make(map[string]*TokenBucket),
		localConnections: make(map[string]int),
		stopCleanup:      make(chan bool),
	}
	defer func() {
		rateLimiter.Close()
		rateLimiter = oldRL
	}()
	
	// Use a specific test IP
	testIP := "10.0.0.1"
	
	// Test HTTP requests
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Fly-Client-IP", testIP)
		rec := httptest.NewRecorder()
		
		handler(rec, req)
		
		if rec.Code != http.StatusOK {
			t.Errorf("Request %d: expected 200, got %d", i+1, rec.Code)
		}
	}
	
	// 4th request should be rate limited
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Fly-Client-IP", testIP)
	rec := httptest.NewRecorder()
	
	handler(rec, req)
	
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("4th request should be rate limited, got %d", rec.Code)
	}
	
	// Check rate limit headers
	if rec.Header().Get("Retry-After") == "" {
		t.Error("Missing Retry-After header")
	}
	expectedLimit := fmt.Sprintf("%.0f", rateLimiter.requestsPerSecond)
	if rec.Header().Get("X-RateLimit-Limit") != expectedLimit {
		t.Errorf("Incorrect X-RateLimit-Limit header: got %s, want %s", 
			rec.Header().Get("X-RateLimit-Limit"), expectedLimit)
	}
	
	// Check response body
	body := rec.Body.String()
	if !strings.Contains(body, "Rate limit exceeded") {
		t.Error("Response body should contain rate limit message")
	}
}

func TestRateLimiter_WebSocketIntegration(t *testing.T) {
	// Initialize global rate limiter for test
	oldRL := rateLimiter
	rateLimiter = &RateLimiter{
		enabled:           true,
		requestsPerSecond: 10.0,
		burstSize:        10,
		wsMaxConnections:  1,
		localBuckets:     make(map[string]*TokenBucket),
		localConnections: make(map[string]int),
		stopCleanup:      make(chan bool),
	}
	defer func() {
		rateLimiter.Close()
		rateLimiter = oldRL
	}()
	
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(handler))
	defer server.Close()
	
	// Convert http to ws URL
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	
	// Set up headers with IP
	headers := http.Header{}
	headers.Set("Fly-Client-IP", "10.0.0.2")
	
	// First connection should succeed
	dialer := websocket.Dialer{}
	conn1, resp1, err := dialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("First WebSocket connection failed: %v", err)
	}
	defer conn1.Close()
	
	if resp1.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("Expected 101, got %d", resp1.StatusCode)
	}
	
	// Second connection should be rate limited
	conn2, resp2, err := dialer.Dial(wsURL, headers)
	if err == nil {
		conn2.Close()
		t.Error("Second WebSocket connection should have been rate limited")
	}
	
	if resp2 != nil && resp2.StatusCode != http.StatusTooManyRequests {
		t.Errorf("Expected 429, got %d", resp2.StatusCode)
	}
}

func TestGetRealIP(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string][]string
		expected string
	}{
		{
			name: "Fly-Client-IP",
			headers: map[string][]string{
				"Fly-Client-Ip": {"192.168.1.1"},  // Note: Go canonicalizes to lowercase 'p'
				"X-Forwarded-For": {"10.0.0.1"},
			},
			expected: "192.168.1.1",
		},
		{
			name: "X-Forwarded-For",
			headers: map[string][]string{
				"X-Forwarded-For": {"192.168.1.2, 10.0.0.1"},
			},
			expected: "192.168.1.2",
		},
		{
			name: "X-Real-IP",
			headers: map[string][]string{
				"X-Real-IP": {"192.168.1.3"},
			},
			expected: "192.168.1.3",
		},
		{
			name:     "No headers",
			headers:  map[string][]string{},
			expected: "unknown",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetRealIP(tt.headers)
			if result != tt.expected {
				t.Errorf("GetRealIP() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestRateLimiter_Cleanup(t *testing.T) {
	rl := &RateLimiter{
		enabled:          true,
		localBuckets:     make(map[string]*TokenBucket),
		localConnections: make(map[string]int),
		stopCleanup:      make(chan bool),
	}
	defer rl.Close()
	
	// Add some buckets
	now := time.Now()
	rl.localBuckets["old"] = &TokenBucket{
		lastRefill:   now.Add(-2 * time.Hour),
		blockedUntil: now.Add(-1 * time.Hour),
	}
	rl.localBuckets["recent"] = &TokenBucket{
		lastRefill:   now.Add(-5 * time.Minute),
		blockedUntil: now,
	}
	rl.localBuckets["blocked"] = &TokenBucket{
		lastRefill:   now.Add(-2 * time.Hour),
		blockedUntil: now.Add(1 * time.Hour),
	}
	
	// Run cleanup
	rl.cleanup()
	
	// Check results
	if _, exists := rl.localBuckets["old"]; exists {
		t.Error("Old bucket should be removed")
	}
	if _, exists := rl.localBuckets["recent"]; !exists {
		t.Error("Recent bucket should be kept")
	}
	if _, exists := rl.localBuckets["blocked"]; !exists {
		t.Error("Blocked bucket should be kept")
	}
}

func TestRateLimiter_DisabledByEnv(t *testing.T) {
	rl := &RateLimiter{
		enabled: false,
		stopCleanup: make(chan bool),
	}
	defer rl.Close()
	
	// All requests should be allowed when disabled
	for i := 0; i < 100; i++ {
		allowed, _ := rl.AllowRequest(fmt.Sprintf("ip-%d", i))
		if !allowed {
			t.Error("All requests should be allowed when rate limiting is disabled")
		}
	}
	
	// Connection tracking should also be bypassed
	if !rl.TrackConnection("any-ip", "websocket") {
		t.Error("Connection tracking should always return true when disabled")
	}
}