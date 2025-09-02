package main

import (
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// TokenBucket represents a token bucket for rate limiting
type TokenBucket struct {
	tokens       float64
	lastRefill   time.Time
	violations   int
	blockedUntil time.Time
}

// RateLimiter manages rate limiting
type RateLimiter struct {
	enabled           bool
	requestsPerSecond float64
	burstSize         int
	blockDuration     time.Duration
	wsMaxConnections  int
	sseMaxConnections int
	
	// Local rate limiting
	localMutex       sync.RWMutex
	localBuckets     map[string]*TokenBucket
	localConnections map[string]int
	
	// Atomic counters
	totalRequests    uint64
	blockedRequests  uint64
	totalConnections int64
	wsConnections    int64
	sseConnections   int64
	
	// Simple metrics using counters
	metricsMutex sync.RWMutex
	metricsWindow time.Time
	requests1Min  int64
	requests5Min  int64
	blocked1Min   int64
	blocked5Min   int64
	wsConnect1Min int64
	wsConnect5Min int64
	sseConnect1Min int64
	sseConnect5Min int64
	
	// Cleanup goroutine control
	stopCleanup chan bool
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter() *RateLimiter {
	enabled := os.Getenv("RATE_LIMIT_ENABLED") != "false"
	
	rps := 2.0
	if val := os.Getenv("RATE_LIMIT_RPS"); val != "" {
		if parsed, err := strconv.ParseFloat(val, 64); err == nil {
			rps = parsed
		}
	}
	
	burst := 10
	if val := os.Getenv("RATE_LIMIT_BURST"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil {
			burst = parsed
		}
	}
	
	blockDuration := 5 * time.Minute
	if val := os.Getenv("RATE_LIMIT_BLOCK_DURATION"); val != "" {
		if seconds, err := strconv.Atoi(val); err == nil {
			blockDuration = time.Duration(seconds) * time.Second
		}
	}
	
	wsMax := 3
	if val := os.Getenv("WEBSOCKET_CONNECTION_LIMIT"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil {
			wsMax = parsed
		}
	}
	
	sseMax := 3
	if val := os.Getenv("SSE_CONNECTION_LIMIT"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil {
			sseMax = parsed
		}
	}
	
	rl := &RateLimiter{
		enabled:           enabled,
		requestsPerSecond: rps,
		burstSize:         burst,
		blockDuration:     blockDuration,
		wsMaxConnections:  wsMax,
		sseMaxConnections: sseMax,
		localBuckets:      make(map[string]*TokenBucket),
		localConnections:  make(map[string]int),
		stopCleanup:       make(chan bool),
		metricsWindow:     time.Now(),
	}
	
	if enabled {
		log.Printf("Rate limiting enabled: %.1f req/s, burst: %d, block: %.0fs", rps, burst, blockDuration.Seconds())
		log.Printf("Connection limits: WebSocket=%d, SSE=%d", wsMax, sseMax)
		go rl.cleanupRoutine()
		go rl.metricsRotationRoutine()
	} else {
		log.Println("Rate limiting disabled")
	}
	
	return rl
}

// Close stops the rate limiter
func (rl *RateLimiter) Close() {
	close(rl.stopCleanup)
}

// CheckRateLimit checks if a request should be rate limited
func (rl *RateLimiter) CheckRateLimit(clientIP string) (bool, time.Duration) {
	if !rl.enabled || clientIP == "unknown" {
		return false, 0
	}
	
	atomic.AddUint64(&rl.totalRequests, 1)
	atomic.AddInt64(&rl.requests1Min, 1)
	
	rl.localMutex.Lock()
	defer rl.localMutex.Unlock()
	
	bucket, exists := rl.localBuckets[clientIP]
	if !exists {
		bucket = &TokenBucket{
			tokens:     float64(rl.burstSize),
			lastRefill: time.Now(),
		}
		rl.localBuckets[clientIP] = bucket
	}
	
	now := time.Now()
	
	// Check if IP is blocked
	if now.Before(bucket.blockedUntil) {
		atomic.AddUint64(&rl.blockedRequests, 1)
		atomic.AddInt64(&rl.blocked1Min, 1)
		return true, time.Until(bucket.blockedUntil)
	}
	
	// Refill tokens
	elapsed := now.Sub(bucket.lastRefill).Seconds()
	bucket.tokens = minFloat(float64(rl.burstSize), bucket.tokens+elapsed*rl.requestsPerSecond)
	bucket.lastRefill = now
	
	// Check if we have tokens
	if bucket.tokens >= 1 {
		bucket.tokens--
		return false, 0
	}
	
	// No tokens available - block the IP
	bucket.violations++
	blockMultiplier := min(bucket.violations, 6) // Cap at 6x
	blockTime := time.Duration(blockMultiplier) * rl.blockDuration
	bucket.blockedUntil = now.Add(blockTime)
	
	atomic.AddUint64(&rl.blockedRequests, 1)
	atomic.AddInt64(&rl.blocked1Min, 1)
	
	log.Printf("Rate limit exceeded for IP %s: blocked for %.0fs (violation #%d)", 
		clientIP, blockTime.Seconds(), bucket.violations)
	
	return true, blockTime
}

// CanConnect checks if a new connection is allowed
func (rl *RateLimiter) CanConnect(clientIP string, connType string) bool {
	if !rl.enabled || clientIP == "unknown" {
		return true
	}
	
	maxConnections := rl.wsMaxConnections
	if connType == "sse" {
		maxConnections = rl.sseMaxConnections
	}
	
	rl.localMutex.RLock()
	currentCount := rl.localConnections[clientIP+":"+connType]
	rl.localMutex.RUnlock()
	
	return currentCount < maxConnections
}

// AddConnection records a new connection
func (rl *RateLimiter) AddConnection(clientIP string, connType string) {
	if !rl.enabled || clientIP == "unknown" {
		return
	}
	
	rl.localMutex.Lock()
	rl.localConnections[clientIP+":"+connType]++
	rl.localMutex.Unlock()
	
	atomic.AddInt64(&rl.totalConnections, 1)
	if connType == "websocket" {
		atomic.AddInt64(&rl.wsConnections, 1)
		atomic.AddInt64(&rl.wsConnect1Min, 1)
	} else if connType == "sse" {
		atomic.AddInt64(&rl.sseConnections, 1)
		atomic.AddInt64(&rl.sseConnect1Min, 1)
	}
}

// RemoveConnection removes a connection
func (rl *RateLimiter) RemoveConnection(clientIP string, connType string) {
	if !rl.enabled || clientIP == "unknown" {
		return
	}
	
	rl.localMutex.Lock()
	key := clientIP + ":" + connType
	if count := rl.localConnections[key]; count > 0 {
		rl.localConnections[key]--
		if rl.localConnections[key] == 0 {
			delete(rl.localConnections, key)
		}
	}
	rl.localMutex.Unlock()
	
	atomic.AddInt64(&rl.totalConnections, -1)
	if connType == "websocket" {
		atomic.AddInt64(&rl.wsConnections, -1)
	} else if connType == "sse" {
		atomic.AddInt64(&rl.sseConnections, -1)
	}
}

// GetStatus returns the current status
func (rl *RateLimiter) GetStatus() RateLimiterStatus {
	rl.localMutex.RLock()
	defer rl.localMutex.RUnlock()
	
	// Count blocked IPs
	blockedCount := 0
	now := time.Now()
	for _, bucket := range rl.localBuckets {
		if now.Before(bucket.blockedUntil) {
			blockedCount++
		}
	}
	
	return RateLimiterStatus{
		Enabled:              rl.enabled,
		UsingLocal:           true,
		RequestsPerSecond:    rl.requestsPerSecond,
		BurstSize:           rl.burstSize,
		BlockDuration:       rl.blockDuration,
		WSMaxConnections:    rl.wsMaxConnections,
		SSEMaxConnections:   rl.sseMaxConnections,
		TrackedIPs:          len(rl.localBuckets),
		BlockedIPs:          blockedCount,
		TotalRequests:       atomic.LoadUint64(&rl.totalRequests),
		BlockedRequests:     atomic.LoadUint64(&rl.blockedRequests),
		CurrentConnections:  atomic.LoadInt64(&rl.totalConnections),
		WSConnections:       atomic.LoadInt64(&rl.wsConnections),
		SSEConnections:      atomic.LoadInt64(&rl.sseConnections),
	}
}

// GetMetrics returns current metrics (simplified for performance)
func (rl *RateLimiter) GetMetrics() Metrics {
	// Get atomic values
	currentConns := atomic.LoadInt64(&rl.totalConnections)
	wsConns := atomic.LoadInt64(&rl.wsConnections)
	sseConns := atomic.LoadInt64(&rl.sseConnections)
	
	// Get counter-based rates
	rl.metricsMutex.RLock()
	req1m := atomic.LoadInt64(&rl.requests1Min)
	req5m := atomic.LoadInt64(&rl.requests5Min)
	block1m := atomic.LoadInt64(&rl.blocked1Min)
	block5m := atomic.LoadInt64(&rl.blocked5Min)
	ws1m := atomic.LoadInt64(&rl.wsConnect1Min)
	ws5m := atomic.LoadInt64(&rl.wsConnect5Min)
	sse1m := atomic.LoadInt64(&rl.sseConnect1Min)
	sse5m := atomic.LoadInt64(&rl.sseConnect5Min)
	rl.metricsMutex.RUnlock()
	
	// Return metrics with clear naming
	return Metrics{
		CurrentConnections:   currentConns,
		WebSocketConnections: wsConns,
		SSEConnections:      sseConns,
		RequestsLastMinute:     float64(req1m),
		RequestsPerMinute5Avg:  float64(req5m) / 5.0,
		BlockedLastMinute:      float64(block1m),
		BlockedPerMinute5Avg:   float64(block5m) / 5.0,
		WSConnectsLastMinute:   float64(ws1m),
		WSConnectsPerMinute5Avg: float64(ws5m) / 5.0,
		SSEConnectsLastMinute:  float64(sse1m),
		SSEConnectsPerMinute5Avg: float64(sse5m) / 5.0,
		TotalBlockedIPs:     0, // Removed for performance
	}
}

// metricsRotationRoutine rotates metrics windows
func (rl *RateLimiter) metricsRotationRoutine() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			rl.rotateMetrics()
		case <-rl.stopCleanup:
			return
		}
	}
}

// rotateMetrics shifts metrics windows
func (rl *RateLimiter) rotateMetrics() {
	rl.metricsMutex.Lock()
	defer rl.metricsMutex.Unlock()
	
	// Shift 1-minute counters to 5-minute counters
	atomic.AddInt64(&rl.requests5Min, atomic.LoadInt64(&rl.requests1Min))
	atomic.AddInt64(&rl.blocked5Min, atomic.LoadInt64(&rl.blocked1Min))
	atomic.AddInt64(&rl.wsConnect5Min, atomic.LoadInt64(&rl.wsConnect1Min))
	atomic.AddInt64(&rl.sseConnect5Min, atomic.LoadInt64(&rl.sseConnect1Min))
	
	// Reset 1-minute counters
	atomic.StoreInt64(&rl.requests1Min, 0)
	atomic.StoreInt64(&rl.blocked1Min, 0)
	atomic.StoreInt64(&rl.wsConnect1Min, 0)
	atomic.StoreInt64(&rl.sseConnect1Min, 0)
	
	// Decay 5-minute counters (keep last 5 minutes)
	atomic.StoreInt64(&rl.requests5Min, atomic.LoadInt64(&rl.requests5Min)*4/5)
	atomic.StoreInt64(&rl.blocked5Min, atomic.LoadInt64(&rl.blocked5Min)*4/5)
	atomic.StoreInt64(&rl.wsConnect5Min, atomic.LoadInt64(&rl.wsConnect5Min)*4/5)
	atomic.StoreInt64(&rl.sseConnect5Min, atomic.LoadInt64(&rl.sseConnect5Min)*4/5)
}

// cleanupRoutine periodically cleans up old entries
func (rl *RateLimiter) cleanupRoutine() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			rl.cleanup()
		case <-rl.stopCleanup:
			return
		}
	}
}

// cleanup removes old entries
func (rl *RateLimiter) cleanup() {
	rl.localMutex.Lock()
	defer rl.localMutex.Unlock()
	
	now := time.Now()
	cutoff := now.Add(-1 * time.Hour)
	
	for ip, bucket := range rl.localBuckets {
		// Remove if not recently active and not blocked
		if bucket.lastRefill.Before(cutoff) && now.After(bucket.blockedUntil) {
			delete(rl.localBuckets, ip)
		}
	}
	
	log.Printf("Cleanup: %d IPs tracked", len(rl.localBuckets))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// Compatibility methods for main.go

// StartHealthReporter is a no-op for compatibility
func (rl *RateLimiter) StartHealthReporter(hostname string) {
	// No longer needed - metrics are calculated on demand
}

// AllowRequest checks if a request is allowed (wrapper for CheckRateLimit)
func (rl *RateLimiter) AllowRequest(clientIP string) (bool, time.Time) {
	blocked, duration := rl.CheckRateLimit(clientIP)
	if blocked {
		return false, time.Now().Add(duration)
	}
	return true, time.Time{}
}

// TrackConnection checks and tracks a new connection
func (rl *RateLimiter) TrackConnection(clientIP string, connType string) bool {
	if !rl.CanConnect(clientIP, connType) {
		return false
	}
	rl.AddConnection(clientIP, connType)
	return true
}

// ReleaseConnection releases a connection (wrapper for RemoveConnection)
func (rl *RateLimiter) ReleaseConnection(clientIP string, connType string) {
	rl.RemoveConnection(clientIP, connType)
}

// GetRealIP extracts the real client IP from request headers
func GetRealIP(headers map[string][]string) string {
	// On Fly.io, Fly-Client-IP is the most reliable
	if ip := getHeader(headers, "Fly-Client-IP"); ip != "" {
		return ip
	}
	
	// Try X-Forwarded-For
	if xff := getHeader(headers, "X-Forwarded-For"); xff != "" {
		// X-Forwarded-For can contain multiple IPs, take the first one
		if idx := len(xff); idx > 0 {
			for i := 0; i < len(xff); i++ {
				if xff[i] == ',' {
					return xff[:i]
				}
			}
		}
		return xff
	}
	
	// Try X-Real-IP
	if ip := getHeader(headers, "X-Real-IP"); ip != "" {
		return ip
	}
	
	// Fallback - this should not happen on Fly.io
	return "unknown"
}

func getHeader(headers map[string][]string, key string) string {
	// Try exact match first
	if values, exists := headers[key]; exists && len(values) > 0 {
		return values[0]
	}
	
	// Try case-insensitive match (Go canonicalizes headers)
	// Common canonicalizations: Fly-Client-Ip -> Fly-Client-IP
	for k, v := range headers {
		if strings.EqualFold(k, key) && len(v) > 0 {
			return v[0]
		}
	}
	
	return ""
}

// RateLimiterStatus represents the current status of the rate limiter
type RateLimiterStatus struct {
	Enabled              bool
	UsingLocal           bool
	RequestsPerSecond    float64
	BurstSize           int
	BlockDuration       time.Duration
	WSMaxConnections    int
	SSEMaxConnections   int
	TrackedIPs          int
	BlockedIPs          int
	TotalRequests       uint64
	BlockedRequests     uint64
	CurrentConnections  int64
	WSConnections       int64
	SSEConnections      int64
}

// Metrics represents the rate limiter metrics
type Metrics struct {
	CurrentConnections   int64
	WebSocketConnections int64
	SSEConnections      int64
	RequestsLastMinute     float64  // Total requests in the last minute
	RequestsPerMinute5Avg  float64  // Average requests per minute over last 5 minutes
	BlockedLastMinute      float64  // Total blocked requests in the last minute
	BlockedPerMinute5Avg   float64  // Average blocked per minute over last 5 minutes
	WSConnectsLastMinute   float64  // Total WS connections in the last minute
	WSConnectsPerMinute5Avg float64  // Average WS connections per minute over last 5 minutes
	SSEConnectsLastMinute  float64  // Total SSE connections in the last minute
	SSEConnectsPerMinute5Avg float64 // Average SSE connections per minute over last 5 minutes
	TotalBlockedIPs     int
}