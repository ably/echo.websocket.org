# Rate Limiting Design

## Overview

The echo server implements local rate limiting to prevent abuse while maintaining high performance.

## Algorithm: Token Bucket

Each client IP receives a token bucket that:
1. Starts with a burst capacity (default: 10 tokens)
2. Refills at a configured rate (default: 2 tokens/second)
3. Consumes 1 token per request
4. Blocks clients when tokens exhausted

## Progressive Blocking

When limits are exceeded:
- 1st violation: 5 minute block
- 2nd violation: 10 minute block
- 3rd violation: 15 minute block
- 4th+ violations: 30 minute block (capped)

## Connection Limits

Separate from request rate limiting:
- WebSocket: Max concurrent connections per IP
- SSE: Max concurrent connections per IP
- Tracked locally in-memory per instance

## Local Rate Limiting

### In-Memory Storage
Each instance maintains its own rate limit state:
- Token bucket state per IP
- Active connection counts per IP
- Block list with expiration times

### Memory Management
- No artificial limits on tracked IPs
- Automatic cleanup every 10 minutes
- Expired entries removed periodically

## Performance Optimizations

### Timeout Management
- WebSocket operations: 30 second timeout
- SSE operations: No timeout (long-lived)
- Cleanup operations: Run periodically

### Memory Efficiency
- Minimal memory footprint per IP
- Efficient cleanup of expired entries
- No external dependencies

## Cleanup Mechanisms

### Automatic Cleanup
1. Local cleanup routine every 10 minutes
2. Expired block entries removed
3. Stale IP entries cleaned up

### Connection Release
- Connection counts decremented on close
- Graceful handling of connection drops
- No leaked connection counts

## Configuration

```bash
RATE_LIMIT_ENABLED=true
RATE_LIMIT_REQUESTS_PER_SECOND=2.0
RATE_LIMIT_BURST_SIZE=10
RATE_LIMIT_BLOCK_DURATION=300
RATE_LIMIT_WEBSOCKET_CONNECTIONS=3
RATE_LIMIT_SSE_CONNECTIONS=3
```

## Monitoring

Health endpoint provides:
- Current open connections (WS and SSE)
- Request rates (1min and 5min windows)
- WebSocket connection rates
- SSE connection rates
- Blocked IP count
- Block rates (1min and 5min windows)

## Fail-Open Behavior

The rate limiter implements fail-open behavior:
- If rate limit state cannot be verified, allow the request
- Prevents blocking legitimate users during system issues
- Prioritizes availability over strict enforcement