# Echo Server

[![CI](https://github.com/ably/echo.websocket.org/actions/workflows/ci.yml/badge.svg)](https://github.com/ably/echo.websocket.org/actions/workflows/ci.yml)
[![Deploy](https://github.com/ably/echo.websocket.org/actions/workflows/fly.yml/badge.svg)](https://github.com/ably/echo.websocket.org/actions/workflows/fly.yml)

This is a very simple HTTP echo server with support for websockets and server-sent
events (SSE), available at https://echo-websocket.fly.dev/

The server is designed for testing HTTP proxies and clients. It echoes
information about HTTP request headers and bodies back to the client.

## Quick Start

```bash
# Clone and build
git clone https://github.com/ably/echo.websocket.org.git
cd echo.websocket.org
go build -o echo-server ./cmd/echo-server

# Run the server
./echo-server

# Test WebSocket connection
curl http://localhost:8080/.ws  # Opens WebSocket test UI in browser
```

## Behavior

- Any messages sent from a websocket client are echoed as a websocket message.
- Visit `/.ws` in a browser for a basic UI to connect and send websocket messages.
- Request `/.sse` to receive the echo response via server-sent events.
- Request any other URL to receive the echo response in plain text.

## Configuration

### Port

The `PORT` environment variable sets the server port, which defaults to `8080`.

### Logging

Set the `LOG_HTTP_HEADERS` environment variable to print request headers to
`STDOUT`. Additionally, set the `LOG_HTTP_BODY` environment variable to print
entire request bodies.

### Server Hostname

Set the `SEND_SERVER_HOSTNAME` environment variable to `false` to prevent the
server from responding with its hostname before echoing the request. The client
may send the `X-Send-Server-Hostname` request header to `true` or `false` to
override this server-wide setting on a per-request basis.

### Connection Timeout

Set the `CONNECTION_TIMEOUT_MINUTES` environment variable to configure the maximum
duration for WebSocket and SSE connections. The default is 10 minutes. For backward
compatibility, `WEBSOCKET_TIMEOUT_MINUTES` is still supported but deprecated.

For WebSocket connections: The timeout is absolute and NOT reset by client activity. 
Connections will be closed after the configured duration regardless of messages being 
sent or received. When the timeout is reached, the server sends a close frame with a 
message indicating the connection has been closed.

For SSE connections: The timeout is absolute from connection start. When the timeout 
is reached, the server sends an error event with the timeout message before closing 
the connection.

### Arbitrary Headers

Set the `SEND_HEADER_<header-name>` variable to send arbitrary additional
headers in the response. Underscores in the variable name are converted to
hyphens in the header. For example, the following environment variables can be
used to disable CORS:

```bash
SEND_HEADER_ACCESS_CONTROL_ALLOW_ORIGIN="*"
SEND_HEADER_ACCESS_CONTROL_ALLOW_METHODS="*"
SEND_HEADER_ACCESS_CONTROL_ALLOW_HEADERS="*"
```

## Testing

The server includes a comprehensive test suite covering HTTP echo, WebSocket, SSE, and timeout functionality.

### Running tests

```bash
# Run all tests
go test -v ./cmd/echo-server

# Run specific test pattern
go test -v ./cmd/echo-server -run TestWebSocket

# Run tests with custom timeout
go test -v ./cmd/echo-server -timeout 30s

# Run tests with coverage
go test -cover ./cmd/echo-server
```

### Test Coverage

The test suite includes:
- WebSocket and SSE echo functionality tests  
- HTTP echo tests
- Connection timeout tests (verifying absolute timeout behavior)
- Configuration tests for environment variables
- Multiple concurrent client tests

Note: WebSocket connections timeout after the configured duration regardless of activity (absolute timeout, not idle timeout).

## Running the server

### Prerequisites

- Go 1.21 or later
- Git

### Running locally

1. Clone the repository:
```bash
git clone https://github.com/ably/echo.websocket.org.git
cd echo.websocket.org
```

2. Build the server:
```bash
go build -o echo-server ./cmd/echo-server
```

3. Run the server:
```bash
# Default port 8080
./echo-server

# Custom port
PORT=10000 ./echo-server

# With connection timeout (in minutes)
CONNECTION_TIMEOUT_MINUTES=5 ./echo-server
```

### Running with Docker

1. Build the Docker image:
```bash
# Build for your current platform
docker build -t echo-server .

# Build for multiple platforms (requires buildx)
docker buildx build --platform linux/amd64,linux/arm64 -t echo-server .
```

2. Run the container:
```bash
# Run on port 8080
docker run -p 8080:8080 echo-server

# Run on custom port
docker run -p 10000:8080 -e PORT=8080 echo-server

# Run with custom timeout
docker run -p 8080:8080 -e CONNECTION_TIMEOUT_MINUTES=5 echo-server
```

### Deploying to Fly.io

This server is configured for deployment on Fly.io. The deployment uses a minimal Docker image built from scratch with just the Go binary.

#### Prerequisites

Before deploying, you need to build the Linux binaries:

```bash
# Build Linux binaries for both architectures
mkdir -p artifacts/build/release/linux/amd64
mkdir -p artifacts/build/release/linux/arm64
GOOS=linux GOARCH=amd64 go build -o artifacts/build/release/linux/amd64/echo-server ./cmd/echo-server
GOOS=linux GOARCH=arm64 go build -o artifacts/build/release/linux/arm64/echo-server ./cmd/echo-server
```

#### Manual Deployment

```bash
# First time setup
fly launch

# Deploy updates (after building binaries)
fly deploy
```

#### Automatic Deployment

The GitHub Actions workflow automatically deploys to Fly.io on every push to the main branch. The workflow:
1. Runs tests to ensure code quality
2. Builds Linux binaries for both amd64 and arm64 architectures
3. Deploys using `flyctl deploy --remote-only`

The deployment uses the Dockerfile which copies the platform-specific binary from `artifacts/build/release/$TARGETPLATFORM/echo-server` to `/bin/echo-server` in the container.

Note: The `FLY_API_TOKEN` secret must be configured in the repository settings for automatic deployment to work.

## License

This project is licensed under the MIT License - see [LICENSE](./LICENSE) for details.

Originally forked from https://github.com/jmalloc/echo-server
