package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
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

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Printf("Echo server listening on port %s.\n", port)

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

	if os.Getenv("LOG_HTTP_BODY") != "" || os.Getenv("LOG_HTTP_HEADERS") != "" {
		fmt.Printf("--------  %s | %s %s\n", req.RemoteAddr, req.Method, req.URL)
	} else {
		fmt.Printf("%s | %s %s\n", req.RemoteAddr, req.Method, req.URL)
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
		serveWebSocket(wr, req, sendServerHostname)
	} else if req.URL.Path == "/.ws" {
		wr.Header().Add("Content-Type", "text/html")
		wr.WriteHeader(200)
		io.WriteString(wr, websocketHTML) // nolint:errcheck
	} else if req.URL.Path == "/.sse" {
		serveSSE(wr, req, sendServerHostname)
	} else {
		serveHTTP(wr, req, sendServerHostname)
	}
}

func serveWebSocket(wr http.ResponseWriter, req *http.Request, sendServerHostname bool) {
	connection, err := upgrader.Upgrade(wr, req, nil)
	if err != nil {
		fmt.Printf("%s | %s\n", req.RemoteAddr, err)
		return
	}

	defer connection.Close()
	fmt.Printf("%s | upgraded to websocket\n", req.RemoteAddr)

	// Get timeout configuration
	timeoutMinutes := defaultConnectionTimeoutMinutes
	if timeoutStr := os.Getenv("CONNECTION_TIMEOUT_MINUTES"); timeoutStr != "" {
		if parsed, err := strconv.Atoi(timeoutStr); err == nil && parsed > 0 {
			timeoutMinutes = parsed
		}
	} else if timeoutStr := os.Getenv("WEBSOCKET_TIMEOUT_MINUTES"); timeoutStr != "" {
		// Backward compatibility
		if parsed, err := strconv.Atoi(timeoutStr); err == nil && parsed > 0 {
			timeoutMinutes = parsed
		}
	}
	timeout := time.Duration(timeoutMinutes) * time.Minute

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
		var messageType int

		// Set initial read deadline
		connection.SetReadDeadline(time.Now().Add(timeout))

		for {
			messageType, message, err = connection.ReadMessage()
			if err != nil {
				// Check if it's a timeout error
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// Send timeout message
					timeoutMsg := fmt.Sprintf("Connection timeout: This connection has been closed after %d minutes. This server is designed for testing with use no longer than %d minutes.", timeoutMinutes, timeoutMinutes)
					connection.WriteControl(websocket.CloseMessage,
						websocket.FormatCloseMessage(websocket.CloseNormalClosure, timeoutMsg),
						time.Now().Add(time.Second))
					fmt.Printf("%s | WebSocket connection timed out after %d minutes\n", req.RemoteAddr, timeoutMinutes)
				}
				break
			}

			// Reset timeout on activity by updating the read deadline
			connection.SetReadDeadline(time.Now().Add(timeout))

			if messageType == websocket.TextMessage {
				fmt.Printf("%s | txt | %s\n", req.RemoteAddr, message)
			} else {
				fmt.Printf("%s | bin | %d byte(s)\n", req.RemoteAddr, len(message))
			}

			err = connection.WriteMessage(messageType, message)
			if err != nil {
				break
			}
		}
	}

	if err != nil {
		fmt.Printf("%s | %s\n", req.RemoteAddr, err)
	}
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

func serveSSE(wr http.ResponseWriter, req *http.Request, sendServerHostname bool) {
	if _, ok := wr.(http.Flusher); !ok {
		http.Error(wr, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	// Get timeout configuration (same as WebSocket)
	timeoutMinutes := defaultConnectionTimeoutMinutes
	if timeoutStr := os.Getenv("CONNECTION_TIMEOUT_MINUTES"); timeoutStr != "" {
		if parsed, err := strconv.Atoi(timeoutStr); err == nil && parsed > 0 {
			timeoutMinutes = parsed
		}
	} else if timeoutStr := os.Getenv("WEBSOCKET_TIMEOUT_MINUTES"); timeoutStr != "" {
		// Backward compatibility
		if parsed, err := strconv.Atoi(timeoutStr); err == nil && parsed > 0 {
			timeoutMinutes = parsed
		}
	}
	timeout := time.Duration(timeoutMinutes) * time.Minute

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
			timeoutMsg := fmt.Sprintf("Connection timeout: This connection has been closed after %d minutes. This server is designed for testing with use no longer than %d minutes.", timeoutMinutes, timeoutMinutes)
			writeSSE(
				wr,
				req,
				&id,
				"error",
				timeoutMsg,
			)
			fmt.Printf("%s | SSE connection timed out after %d minutes\n", req.RemoteAddr, timeoutMinutes)
			return
		case t := <-ticker.C:
			writeSSE(
				wr,
				req,
				&id,
				"time",
				t.Format(time.RFC3339),
			)
			// Reset timeout on activity (each second when we send time events)
			timer.Reset(timeout)
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
