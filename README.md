# Echo Server

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

For WebSocket connections: The timeout is reset whenever a message is received from 
the client. When the timeout is reached, the server sends a close frame with a message 
indicating the connection has been closed.

For SSE connections: The timeout is reset with each event sent (every second). When 
the timeout is reached, the server sends an error event with the timeout message 
before closing the connection.

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

For more detailed testing information, see [TESTING.md](./TESTING.md).

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

This server is configured for deployment on Fly.io:

```bash
# First time setup
fly launch

# Deploy updates
fly deploy
```

Note: Deployment requires building platform-specific binaries first. The GitHub Actions workflow handles this automatically on push to main.

## License

This project is licensed under the MIT License - see [LICENSE](./LICENSE) for details.

Originally forked from https://github.com/jmalloc/echo-server
