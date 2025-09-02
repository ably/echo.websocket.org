# Echo WebSocket Server Architecture

## Overview

The echo.websocket.org service is a high-performance WebSocket and Server-Sent Events (SSE) echo server designed for testing and debugging real-time connections. It runs on Fly.io with local rate limiting.

## Components

### Echo Server (echo-websocket)
- **Language**: Go 1.21+
- **Framework**: Standard library + Gorilla WebSocket
- **Deployment**: 2 instances on Fly.io
- **Region**: Primary in LHR (London)
- **Resources**: 4 shared CPUs, 1GB RAM per instance

## Request Flow

```
Client → Fly Proxy → App Instance → Local Rate Limiter
                           ↓
                    WebSocket/SSE/HTTP Response
```

## Rate Limiting Architecture

### Token Bucket Algorithm
- Each IP gets a token bucket with configurable burst size
- Tokens refill at a configured rate (requests/second)
- Exceeded limits result in progressive blocking

### Local State
- Each instance maintains its own rate limit state
- In-memory storage with automatic cleanup
- No external dependencies

### Connection Limits
- Per-IP limits for WebSocket and SSE connections
- Prevents resource exhaustion from single clients

## Health Monitoring

### Instance Health
Each instance provides real-time metrics:
- Active connections (WebSocket/SSE)
- Request rates (1min/5min windows)
- Blocked IPs and block rates
- Connection rates by type

## Auto-scaling

Fly.io handles auto-scaling based on:
- Connection count (soft limit: 2500, hard limit: 5000)
- Minimum 1 instance always running
- Suspend mode for fast restarts (~300ms)

## Failure Handling

### Fail-Open Behavior
- If rate limit state cannot be verified, allow the request
- Prevents blocking legitimate users during system issues
- Prioritizes availability over strict enforcement

### Instance Failures
1. Fly.io automatically restarts failed instances
2. Load balancer routes around unhealthy instances
3. Each instance operates independently

## Performance Characteristics

### Targets
- HTTP response time: < 100ms
- WebSocket connections: 5000+ per instance
- Memory usage: < 1GB per instance
- No external service dependencies

### Optimizations
1. Efficient in-memory storage
2. Periodic cleanup of expired entries
3. Minimal memory footprint per tracked IP

## Configuration

### Environment Variables
See README.md for full list. Key variables:
- `RATE_LIMIT_*`: Rate limiting parameters
- `CONNECTION_TIMEOUT_MINUTES`: Long-lived connection timeout

### Fly.io Configuration
- `fly.toml`: Main app configuration
- Auto-scaling rules and health checks
- Resource allocation

## Monitoring

### Endpoints
- `/`: Echo HTTP request
- `/.ws`: WebSocket test interface
- `/.sse`: Server-Sent Events stream
- `/.health`: Simple health check (UP/DOWN status)
- `/.stats`: Detailed metrics (optional basic auth)

### Health Monitoring
- `/.health`: Simple status check for load balancers
  - Returns UP/DEGRADED status with reason
  - No authentication required
  - Lightweight response

- `/.stats`: Comprehensive metrics endpoint
  - Optional basic auth protection
  - Detailed rate limiter statistics
  - Connection metrics
  - Request and block rates
  - Instance information