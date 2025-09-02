package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

const (
	// defaultConnectionTimeoutMinutes is the default timeout for long-lived connections (WebSocket and SSE)
	defaultConnectionTimeoutMinutes = 10
)

var rateLimiter *RateLimiter

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Initialize rate limiter
	rateLimiter = NewRateLimiter()
	defer rateLimiter.Close()
	
	// Start health reporting
	hostname, _ := os.Hostname()
	rateLimiter.StartHealthReporter(hostname)

	fmt.Printf("🚀 Echo server v2.1 (IP-FIXED) listening on port %s.\n", port)

	err := http.ListenAndServe(
		":"+port,
		h2c.NewHandler(
			http.HandlerFunc(handler),
			&http2.Server{},
		),
	)
	if err != nil {
		panic(err)
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool {
		return true
	},
}

func handler(wr http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()

	// Extract real IP for rate limiting
	clientIP := GetRealIP(req.Header)
	
	// Check rate limit
	allowed, blockedUntil := rateLimiter.AllowRequest(clientIP)
	if !allowed {
		fmt.Printf("🚫 RATE_LIMIT_v2: %s | %s %s (blocked until %s)\n", 
			clientIP, req.Method, req.URL, blockedUntil.Format(time.RFC3339))
		handleRateLimitExceeded(wr, req, blockedUntil, clientIP)
		return
	}
	
	// Debug logging with real client IP
	if os.Getenv("DEBUG_RATE_LIMIT") != "" {
		fmt.Printf("✅ RATE_LIMIT: %s | %s %s (allowed)\n", clientIP, req.Method, req.URL)
	}

	if os.Getenv("LOG_HTTP_BODY") != "" || os.Getenv("LOG_HTTP_HEADERS") != "" {
		fmt.Printf("--------  %s | %s %s\n", clientIP, req.Method, req.URL)
	} else {
		fmt.Printf("%s | %s %s\n", clientIP, req.Method, req.URL)
	}

	if os.Getenv("LOG_HTTP_HEADERS") != "" {
		fmt.Printf("Headers\n")
		printHeaders(os.Stdout, req.Header)
	}

	if os.Getenv("LOG_HTTP_BODY") != "" {
		buf := &bytes.Buffer{}
		buf.ReadFrom(req.Body) // nolint:errcheck

		if buf.Len() != 0 {
			w := hex.Dumper(os.Stdout)
			w.Write(buf.Bytes()) // nolint:errcheck
			w.Close()
		}

		// Replace original body with buffered version so it's still sent to the
		// browser.
		req.Body.Close()
		req.Body = io.NopCloser(
			bytes.NewReader(buf.Bytes()),
		)
	}

	sendServerHostnameString := os.Getenv("SEND_SERVER_HOSTNAME")
	if v := req.Header.Get("X-Send-Server-Hostname"); v != "" {
		sendServerHostnameString = v
	}

	sendServerHostname := !strings.EqualFold(
		sendServerHostnameString,
		"false",
	)

	for _, line := range os.Environ() {
		parts := strings.SplitN(line, "=", 2)
		key, value := parts[0], parts[1]

		if name, ok := strings.CutPrefix(key, `SEND_HEADER_`); ok {
			wr.Header().Set(
				strings.ReplaceAll(name, "_", "-"),
				value,
			)
		}
	}

	if websocket.IsWebSocketUpgrade(req) {
		serveWebSocket(wr, req, sendServerHostname, clientIP)
	} else if req.URL.Path == "/.ws" {
		wr.Header().Add("Content-Type", "text/html")
		wr.WriteHeader(200)
		io.WriteString(wr, websocketHTML) // nolint:errcheck
	} else if req.URL.Path == "/.sse" {
		serveSSE(wr, req, sendServerHostname, clientIP)
	} else if req.URL.Path == "/.health" {
		serveHealthCheck(wr, req)
	} else if req.URL.Path == "/.stats" {
		serveStats(wr, req)
	} else {
		serveHTTP(wr, req, sendServerHostname)
	}
}

func serveWebSocket(wr http.ResponseWriter, req *http.Request, sendServerHostname bool, clientIP string) {
	// Check WebSocket connection limit
	if !rateLimiter.TrackConnection(clientIP, "websocket") {
		wr.WriteHeader(http.StatusTooManyRequests)
		wr.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(wr, "Too many WebSocket connections from your IP (limit: %d). Please try again later.", 
			rateLimiter.wsMaxConnections)
		return
	}
	defer rateLimiter.ReleaseConnection(clientIP, "websocket")
	
	connection, err := upgrader.Upgrade(wr, req, nil)
	if err != nil {
		fmt.Printf("%s | %s\n", req.RemoteAddr, err)
		return
	}

	defer connection.Close()
	fmt.Printf("%s | upgraded to websocket\n", req.RemoteAddr)

	// Get timeout configuration
	timeoutMinutes := float64(defaultConnectionTimeoutMinutes)
	if timeoutStr := os.Getenv("CONNECTION_TIMEOUT_MINUTES"); timeoutStr != "" {
		if parsed, err := strconv.ParseFloat(timeoutStr, 64); err == nil && parsed > 0 {
			timeoutMinutes = parsed
		}
	} else if timeoutStr := os.Getenv("WEBSOCKET_TIMEOUT_MINUTES"); timeoutStr != "" {
		// Backward compatibility
		if parsed, err := strconv.ParseFloat(timeoutStr, 64); err == nil && parsed > 0 {
			timeoutMinutes = parsed
		}
	}
	timeout := time.Duration(timeoutMinutes * float64(time.Minute))

	var message []byte

	if sendServerHostname {
		host, err := os.Hostname()
		if err == nil {
			message = []byte(fmt.Sprintf("Request served by %s", host))
		} else {
			message = []byte(fmt.Sprintf("Server hostname unknown: %s", err.Error()))
		}
	}

	err = connection.WriteMessage(websocket.TextMessage, message)
	if err == nil {
		// Create channels for communication
		type wsMessage struct {
			messageType int
			message     []byte
			err         error
		}
		messageChan := make(chan wsMessage)

		// Channel to signal when to stop reading
		done := make(chan bool)

		// Start goroutine to read messages
		go func() {
			for {
				messageType, message, err := connection.ReadMessage()
				select {
				case messageChan <- wsMessage{messageType, message, err}:
				case <-done:
					return
				}
				if err != nil {
					return
				}
			}
		}()

		// Create timer for absolute timeout
		timeoutTimer := time.NewTimer(timeout)
		defer timeoutTimer.Stop()
		// Ensure done channel is closed when function returns to prevent goroutine leak
		defer close(done)

		for {
			select {
			case <-timeoutTimer.C:
				// Timeout occurred
				timeoutMsg := fmt.Sprintf("Connection timeout: This connection has been closed after %.2f minutes. This server is designed for testing with use no longer than %.2f minutes.", timeoutMinutes, timeoutMinutes)

				// Send timeout message as a regular text message first (for better browser compatibility)
				_ = connection.WriteMessage(websocket.TextMessage, []byte(timeoutMsg))

				// Then send close frame with timeout
				_ = connection.WriteControl(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, timeoutMsg),
					time.Now().Add(time.Second))

				// Close the connection (done channel will be closed by defer)
				connection.Close()

				fmt.Printf("%s | WebSocket connection timed out after %.2f minutes\n", req.RemoteAddr, timeoutMinutes)
				return

			case msg := <-messageChan:
				if msg.err != nil {
					fmt.Printf("%s | %s\n", req.RemoteAddr, msg.err)
					return
				}

				if msg.messageType == websocket.TextMessage {
					fmt.Printf("%s | txt | %s\n", req.RemoteAddr, msg.message)
				} else {
					fmt.Printf("%s | bin | %d byte(s)\n", req.RemoteAddr, len(msg.message))
				}

				if writeErr := connection.WriteMessage(msg.messageType, msg.message); writeErr != nil {
					fmt.Printf("%s | %s\n", req.RemoteAddr, writeErr)
					return
				}
			}
		}
	}
}

func handleRateLimitExceeded(wr http.ResponseWriter, req *http.Request, blockedUntil time.Time, clientIP string) {
	retryAfter := int(time.Until(blockedUntil).Seconds())
	if retryAfter < 1 {
		retryAfter = 1
	}
	
	// Set rate limit headers
	wr.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	wr.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%.0f", rateLimiter.requestsPerSecond))
	wr.Header().Set("X-RateLimit-Remaining", "0")
	wr.Header().Set("X-RateLimit-Reset", strconv.FormatInt(blockedUntil.Unix(), 10))
	
	// Different response based on transport
	if websocket.IsWebSocketUpgrade(req) {
		// For WebSocket, return error before upgrade
		wr.Header().Set("Content-Type", "text/plain")
		wr.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprintf(wr, "Rate limit exceeded. You are blocked for %d seconds. Please slow down your request rate.", retryAfter)
	} else if req.URL.Path == "/.sse" {
		// For SSE, send error event
		wr.Header().Set("Content-Type", "text/event-stream")
		wr.Header().Set("Cache-Control", "no-cache")
		wr.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprintf(wr, "event: error\n")
		fmt.Fprintf(wr, "data: Rate limit exceeded. Blocked for %d seconds.\n", retryAfter)
		fmt.Fprintf(wr, "retry: %d000\n\n", retryAfter)
		if flusher, ok := wr.(http.Flusher); ok {
			flusher.Flush()
		}
	} else {
		// For HTTP, return plain text
		wr.Header().Set("Content-Type", "text/plain; charset=utf-8")
		wr.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprintf(wr, "Rate limit exceeded.\n\n")
		fmt.Fprintf(wr, "You have been temporarily blocked for %d seconds.\n", retryAfter)
		fmt.Fprintf(wr, "Current limit: %.0f requests per second with burst of %d.\n", 
			rateLimiter.requestsPerSecond, rateLimiter.burstSize)
		fmt.Fprintf(wr, "Please reduce your request rate.\n")
	}
	
	fmt.Printf("%s | Rate limited (blocked for %ds)\n", clientIP, retryAfter)
}

func serveHTTP(wr http.ResponseWriter, req *http.Request, sendServerHostname bool) {
	wr.Header().Add("Content-Type", "text/plain; charset=utf-8")
	wr.WriteHeader(200)

	if sendServerHostname {
		hostname, err := os.Hostname()
		if err == nil {
			fmt.Fprintf(wr, "Request served by %s\n\n", hostname)
		} else {
			fmt.Fprintf(wr, "Server hostname unknown: %s\n\n", err.Error())
		}
	}

	// Write the echoed request first (maintaining the core functionality)
	writeRequest(wr, req)

	// Get the host for dynamic URLs
	scheme := "http"
	if req.TLS != nil || req.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := req.Host

	// Add subtle footer with helpful links
	fmt.Fprintln(wr, "\n----------------------------------------------------------------------")
	fmt.Fprintln(wr, "         __      __   _                 _        _                    ")
	fmt.Fprintln(wr, "         \\ \\    / /__| |__  ___ ___  __| |_____| |_                  ")
	fmt.Fprintln(wr, "          \\ \\/\\/ / -_) '_ \\(_-</ _ \\/ _| / / -_)  _|                 ")
	fmt.Fprintln(wr, "           \\_/\\_/\\___|_.__//__/\\___/\\__|_\\_\\___|\\__|                 ")
	fmt.Fprintln(wr, "")
	fmt.Fprintf(wr, "  WebSocket UI: %s://%s/.ws  |  SSE: %s://%s/.sse\n", scheme, host, scheme, host)
	fmt.Fprintln(wr, "  Learn more: https://websocket.org/tools/websocket-echo-server")
	fmt.Fprintln(wr, "----------------------------------------------------------------------")
}

func serveSSE(wr http.ResponseWriter, req *http.Request, sendServerHostname bool, clientIP string) {
	// Check SSE connection limit
	if !rateLimiter.TrackConnection(clientIP, "sse") {
		http.Error(wr, fmt.Sprintf("Too many SSE connections from your IP (limit: %d). Please try again later.", 
			rateLimiter.sseMaxConnections), http.StatusTooManyRequests)
		return
	}
	defer rateLimiter.ReleaseConnection(clientIP, "sse")
	
	if _, ok := wr.(http.Flusher); !ok {
		http.Error(wr, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	// Get timeout configuration (same as WebSocket)
	timeoutMinutes := float64(defaultConnectionTimeoutMinutes)
	if timeoutStr := os.Getenv("CONNECTION_TIMEOUT_MINUTES"); timeoutStr != "" {
		if parsed, err := strconv.ParseFloat(timeoutStr, 64); err == nil && parsed > 0 {
			timeoutMinutes = parsed
		}
	} else if timeoutStr := os.Getenv("WEBSOCKET_TIMEOUT_MINUTES"); timeoutStr != "" {
		// Backward compatibility
		if parsed, err := strconv.ParseFloat(timeoutStr, 64); err == nil && parsed > 0 {
			timeoutMinutes = parsed
		}
	}
	timeout := time.Duration(timeoutMinutes * float64(time.Minute))

	var echo strings.Builder
	writeRequest(&echo, req)

	wr.Header().Set("Content-Type", "text/event-stream")
	wr.Header().Set("Cache-Control", "no-cache")
	wr.Header().Set("Connection", "keep-alive")
	wr.Header().Set("Access-Control-Allow-Origin", "*")

	var id int

	// Write an event about the server that is serving this request.
	if sendServerHostname {
		if host, err := os.Hostname(); err == nil {
			writeSSE(
				wr,
				req,
				&id,
				"server",
				host,
			)
		}
	}

	// Write an event that echoes back the request.
	writeSSE(
		wr,
		req,
		&id,
		"request",
		echo.String(),
	)

	// Set up timeout timer
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	// Then send a counter event every second.
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-req.Context().Done():
			return
		case <-timer.C:
			// Send timeout message via SSE before closing
			timeoutMsg := fmt.Sprintf("Connection timeout: This connection has been closed after %.2f minutes. This server is designed for testing with use no longer than %.2f minutes.", timeoutMinutes, timeoutMinutes)
			writeSSE(
				wr,
				req,
				&id,
				"error",
				timeoutMsg,
			)
			fmt.Printf("%s | SSE connection timed out after %.2f minutes\n", req.RemoteAddr, timeoutMinutes)
			return
		case t := <-ticker.C:
			writeSSE(
				wr,
				req,
				&id,
				"time",
				t.Format(time.RFC3339),
			)
			// Don't reset timeout - SSE should timeout after the configured duration
			// regardless of server-sent events
		}
	}
}

// writeSSE sends a server-sent event and logs it to the console.
func writeSSE(
	wr http.ResponseWriter,
	req *http.Request,
	id *int,
	event, data string,
) {
	*id++
	writeSSEField(wr, req, "event", event)
	writeSSEField(wr, req, "data", data)
	writeSSEField(wr, req, "id", strconv.Itoa(*id))
	fmt.Fprintf(wr, "\n")
	wr.(http.Flusher).Flush()
}

// writeSSEField sends a single field within an event.
func writeSSEField(
	wr http.ResponseWriter,
	req *http.Request,
	k, v string,
) {
	for _, line := range strings.Split(v, "\n") {
		fmt.Fprintf(wr, "%s: %s\n", k, line)
		fmt.Printf("%s | sse | %s: %s\n", req.RemoteAddr, k, line)
	}
}

// writeRequest writes request headers to w.
func writeRequest(w io.Writer, req *http.Request) {
	fmt.Fprintf(w, "%s %s %s\n", req.Method, req.URL, req.Proto)
	fmt.Fprintln(w, "")

	fmt.Fprintf(w, "Host: %s\n", req.Host)
	printHeaders(w, req.Header)

	var body bytes.Buffer
	io.Copy(&body, req.Body) // nolint:errcheck

	if body.Len() > 0 {
		fmt.Fprintln(w, "")
		body.WriteTo(w) // nolint:errcheck
	}
}

func printHeaders(w io.Writer, h http.Header) {
	sortedKeys := make([]string, 0, len(h))

	for key := range h {
		sortedKeys = append(sortedKeys, key)
	}

	sort.Strings(sortedKeys)

	for _, key := range sortedKeys {
		for _, value := range h[key] {
			fmt.Fprintf(w, "%s: %s\n", key, value)
		}
	}
}

// getMemoryLimit returns the container memory limit in bytes, or system memory if not containerized
func getMemoryLimit() uint64 {
	// Try to read cgroup v1 memory limit (works on most container environments)
	if data, err := os.ReadFile("/sys/fs/cgroup/memory/memory.limit_in_bytes"); err == nil {
		if limit, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64); err == nil {
			// Check if it's not the "unlimited" value (9223372036854771712 on 64-bit systems)
			if limit < uint64(1<<62) {
				return limit
			}
		}
	}
	
	// Try cgroup v2 (newer systems)
	if data, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
		if trimmed := strings.TrimSpace(string(data)); trimmed != "max" {
			if limit, err := strconv.ParseUint(trimmed, 10, 64); err == nil {
				return limit
			}
		}
	}
	
	// Fallback to system memory
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	return memStats.Sys
}

// calculateIPThreshold calculates the max number of IPs based on available memory
func calculateIPThreshold() int {
	memLimit := getMemoryLimit()
	
	// Get config from environment with defaults
	memPercent := 25.0 // Default: use 25% of memory for IP tracking
	if val := os.Getenv("HEALTH_IP_MEMORY_PERCENT"); val != "" {
		if parsed, err := strconv.ParseFloat(val, 64); err == nil && parsed > 0 && parsed <= 100 {
			memPercent = parsed
		}
	}
	
	degradedPercent := 80.0 // Default: degraded at 80% of max IPs
	if val := os.Getenv("HEALTH_DEGRADED_IP_PERCENT"); val != "" {
		if parsed, err := strconv.ParseFloat(val, 64); err == nil && parsed > 0 && parsed <= 100 {
			degradedPercent = parsed
		}
	}
	
	// Calculate thresholds
	// Assume ~150 bytes per IP (conservative estimate including map overhead)
	bytesPerIP := 150
	maxMemoryForIPs := uint64(float64(memLimit) * (memPercent / 100.0))
	maxIPs := maxMemoryForIPs / uint64(bytesPerIP)
	degradedThreshold := int(float64(maxIPs) * (degradedPercent / 100.0))
	
	// Log the calculation for visibility
	fmt.Printf("Memory-based IP thresholds: memory=%dMB, using %.0f%% for IPs, degraded at %.0f%% = %d IPs\n",
		memLimit/(1024*1024), memPercent, degradedPercent, degradedThreshold)
	
	return degradedThreshold
}

// serveHealthCheck returns simplified health status
func serveHealthCheck(wr http.ResponseWriter, req *http.Request) {
	// Simple health check - server is up if we can respond
	status := "UP"
	reason := "Server is running"
	httpStatus := http.StatusOK
	
	// For an echo server expecting many random clients, high IP counts are normal
	// Only consider degraded if we're seeing signs of actual problems
	if rateLimiter != nil && rateLimiter.enabled {
		rlStatus := rateLimiter.GetStatus()
		metrics := rateLimiter.GetMetrics()
		
		// Calculate dynamic thresholds
		ipThreshold := calculateIPThreshold()
		
		// Get block rate threshold from env
		blockRateThreshold := 0.5 // Default: 50% block rate
		if val := os.Getenv("HEALTH_DEGRADED_BLOCK_RATE"); val != "" {
			if parsed, err := strconv.ParseFloat(val, 64); err == nil && parsed > 0 && parsed <= 100 {
				blockRateThreshold = parsed / 100.0
			}
		}
		
		// Get connection threshold from env
		connThreshold := 0.9 // Default: 90% of max connections
		if val := os.Getenv("HEALTH_DEGRADED_CONNECTION_PERCENT"); val != "" {
			if parsed, err := strconv.ParseFloat(val, 64); err == nil && parsed > 0 && parsed <= 100 {
				connThreshold = parsed / 100.0
			}
		}
		
		// Check degraded conditions
		if rlStatus.TrackedIPs > ipThreshold {
			status = "DEGRADED"
			reason = fmt.Sprintf("High number of tracked IPs: %d (threshold: %d)", rlStatus.TrackedIPs, ipThreshold)
		} else if rlStatus.BlockedRequests > 0 && rlStatus.TotalRequests > 0 {
			blockRate := float64(rlStatus.BlockedRequests) / float64(rlStatus.TotalRequests)
			if blockRate > blockRateThreshold {
				status = "DEGRADED"
				reason = fmt.Sprintf("High block rate: %.1f%% (threshold: %.0f%%)", blockRate*100, blockRateThreshold*100)
			}
		} else if metrics.CurrentConnections > int64(float64(15000)*connThreshold) {
			// Near connection limit (15000 is our hard limit from fly.toml)
			status = "DEGRADED"
			reason = fmt.Sprintf("Near connection limit: %d/15000 (%.0f%%)", 
				metrics.CurrentConnections, float64(metrics.CurrentConnections)/150.0)
		}
	}
	
	wr.Header().Set("Content-Type", "application/json")
	wr.WriteHeader(httpStatus)
	
	fmt.Fprintf(wr, `{"status":"%s","reason":"%s"}`, status, reason)
}

// serveStats returns detailed metrics with optional basic auth
func serveStats(wr http.ResponseWriter, req *http.Request) {
	// Check if basic auth is configured
	statsUser := os.Getenv("STATS_USERNAME")
	statsPass := os.Getenv("STATS_PASSWORD")
	
	// If credentials are configured, require authentication
	if statsUser != "" && statsPass != "" {
		auth := req.Header.Get("Authorization")
		if auth == "" {
			wr.Header().Set("WWW-Authenticate", `Basic realm="Stats"`)
			http.Error(wr, "Unauthorized", http.StatusUnauthorized)
			return
		}
		
		const prefix = "Basic "
		if !strings.HasPrefix(auth, prefix) {
			http.Error(wr, "Unauthorized", http.StatusUnauthorized)
			return
		}
		
		decoded, err := base64.StdEncoding.DecodeString(auth[len(prefix):])
		if err != nil {
			http.Error(wr, "Unauthorized", http.StatusUnauthorized)
			return
		}
		
		credentials := strings.SplitN(string(decoded), ":", 2)
		if len(credentials) != 2 {
			http.Error(wr, "Unauthorized", http.StatusUnauthorized)
			return
		}
		
		// Use constant-time comparison to prevent timing attacks
		userMatch := subtle.ConstantTimeCompare([]byte(credentials[0]), []byte(statsUser)) == 1
		passMatch := subtle.ConstantTimeCompare([]byte(credentials[1]), []byte(statsPass)) == 1
		
		if !userMatch || !passMatch {
			http.Error(wr, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}
	
	// Return detailed stats
	status := rateLimiter.GetStatus()
	metrics := rateLimiter.GetMetrics()
	hostname := getHostname()
	
	wr.Header().Set("Content-Type", "application/json")
	wr.WriteHeader(http.StatusOK)
	
	fmt.Fprintf(wr, `{
  "status": "healthy",
  "instance": {
    "hostname": %q,
    "rate_limiter": {
      "enabled": %t,
      "requests_per_second": %.1f,
      "burst_size": %d,
      "block_duration_seconds": %d,
      "websocket_connection_limit": %d,
      "sse_connection_limit": %d,
      "tracked_ips": %d,
      "blocked_ips": %d,
      "total_requests": %d,
      "blocked_requests": %d
    },
    "metrics": {
      "current_connections": %d,
      "websocket_connections": %d,
      "sse_connections": %d,
      "requests_last_minute": %.0f,
      "requests_per_minute_5min_avg": %.1f,
      "blocked_last_minute": %.0f,
      "blocked_per_minute_5min_avg": %.1f,
      "ws_connects_last_minute": %.0f,
      "ws_connects_per_minute_5min_avg": %.1f,
      "sse_connects_last_minute": %.0f,
      "sse_connects_per_minute_5min_avg": %.1f,
      "total_blocked_ips": %d
    },
    "version": "3.0"
  }
}`,
		hostname,
		status.Enabled,
		status.RequestsPerSecond,
		status.BurstSize,
		int(status.BlockDuration.Seconds()),
		status.WSMaxConnections,
		status.SSEMaxConnections,
		status.TrackedIPs,
		status.BlockedIPs,
		status.TotalRequests,
		status.BlockedRequests,
		metrics.CurrentConnections,
		metrics.WebSocketConnections,
		metrics.SSEConnections,
		metrics.RequestsLastMinute,
		metrics.RequestsPerMinute5Avg,
		metrics.BlockedLastMinute,
		metrics.BlockedPerMinute5Avg,
		metrics.WSConnectsLastMinute,
		metrics.WSConnectsPerMinute5Avg,
		metrics.SSEConnectsLastMinute,
		metrics.SSEConnectsPerMinute5Avg,
		metrics.TotalBlockedIPs,
	)
}

func getHostname() string {
	hostname, _ := os.Hostname()
	return hostname
}
