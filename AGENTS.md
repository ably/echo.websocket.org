# AI Agent Guidelines for echo.websocket.org

This document provides essential context and guidelines for AI coding agents working on this project.

## Project Overview

A high-performance WebSocket/SSE echo server running on Fly.io with local rate limiting.

## Critical Requirements

### Before Considering Work Complete

1. **Run tests**: `go test ./...` must pass
2. **Run linter**: `golangci-lint run` must have no errors
3. **Build verification**: `go build ./cmd/echo-server` must succeed
4. **Documentation**: Update README.md and docs/ if functionality changes

### Known Issues & Patterns

1. **Connection Management**: Ensure proper cleanup on connection close
2. **Memory Management**: Periodic cleanup of expired rate limit entries
3. **Fly.io Networking**: Load balancing across instances

## Architecture Decisions

- **Rate Limiting**: Local in-memory with fail-open behavior
- **Connection Limits**: Per-IP limits to prevent abuse
- **Health Monitoring**: Simple health check at /.health, detailed stats at /.stats
- **Deployment**: 2 instances on Fly.io with auto-scaling

## Development Workflow

1. Make changes in feature branch
2. Run tests locally
3. Check linting
4. Update documentation in `docs/` if needed
5. Update README.md if user-facing changes
6. Deploy to staging first if available

## Documentation Responsibilities

- Keep `docs/` folder updated with architecture changes
- Update README.md for user-facing features
- Document environment variables in both README and code
- Add inline comments for complex logic only

## Testing

```bash
# Run all tests
go test ./...

# Run with race detection
go test -race ./...

# Run specific test
go test -run TestRateLimiter ./cmd/echo-server
```

## Deployment

```bash
# Deploy app
fly deploy -a echo-websocket

# Set secrets
fly secrets set KEY=value -a echo-websocket
```

## Performance Targets

- Response time: < 100ms for HTTP requests
- WebSocket connections: 5000+ per instance
- Memory usage: < 1GB per instance
- No external service dependencies

## File Structure

```
/cmd/echo-server/    - Main application code
/docs/              - Architecture and design docs
```