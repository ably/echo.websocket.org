FROM golang:1.23-alpine AS builder

WORKDIR /app

# Copy go.mod and go.sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the application with static linking
RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-extldflags "-static"' -o echo-server ./cmd/echo-server

# Use alpine for the final stage to allow setting ulimits
FROM alpine:3.18

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates

# Copy the binary from builder stage
COPY --from=builder /app/echo-server /bin/echo-server

# Create a shell script to set ulimits and run the server
RUN echo '#!/bin/sh' > /entrypoint.sh && \
    echo 'ulimit -n 65535' >> /entrypoint.sh && \
    echo 'exec /bin/echo-server' >> /entrypoint.sh && \
    chmod +x /entrypoint.sh

ENV PORT 8080
EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]