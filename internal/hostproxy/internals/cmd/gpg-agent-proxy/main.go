// gpg-agent-proxy forwards GPG agent requests to the clawker host proxy.
// This allows containers to use the host's GPG agent even when direct socket
// mounting has permission issues (e.g., macOS Docker Desktop).
//
// The proxy creates a Unix socket at ~/.gnupg/S.gpg-agent and forwards
// Assuan protocol messages to the host proxy's /gpg/agent endpoint.
//
// Build: go build -o gpg-agent-proxy main.go
// Usage: gpg-agent-proxy (runs in background, creates GPG socket)
//
// Environment:
//   - CLAWKER_HOST_PROXY: Required. URL of the host proxy (e.g., http://host.docker.internal:18374)
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

// maxMessageSize is the maximum size of a GPG agent message (64KB).
// This matches the limit in the host proxy's /gpg/agent endpoint.
const maxMessageSize = 64 * 1024

// gpgAgentRequest is the JSON request body for POST /gpg/agent.
type gpgAgentRequest struct {
	// Data is the base64-encoded GPG agent protocol message
	Data string `json:"data"`
}

// gpgAgentResponse is the JSON response body from POST /gpg/agent.
type gpgAgentResponse struct {
	// Success indicates whether the request was processed successfully
	Success bool `json:"success"`
	// Data is the base64-encoded response from the GPG agent (present on success)
	Data string `json:"data,omitempty"`
	// Error contains the error message (present on failure)
	Error string `json:"error,omitempty"`
}

func main() {
	hostProxy := os.Getenv("CLAWKER_HOST_PROXY")
	if hostProxy == "" {
		fmt.Fprintln(os.Stderr, "error: CLAWKER_HOST_PROXY not set")
		os.Exit(1)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot get home directory: %v\n", err)
		os.Exit(1)
	}

	// Create .gnupg directory with proper permissions
	gnupgDir := filepath.Join(homeDir, ".gnupg")
	if err := os.MkdirAll(gnupgDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot create .gnupg directory: %v\n", err)
		os.Exit(1)
	}

	// GPG looks for its socket at S.gpg-agent in the gnupg directory
	socketPath := filepath.Join(gnupgDir, "S.gpg-agent")

	// Remove existing socket if present
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "error: cannot remove existing socket: %v\n", err)
		os.Exit(1)
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot create socket: %v\n", err)
		os.Exit(1)
	}
	defer listener.Close()
	defer os.Remove(socketPath)

	// Set socket permissions
	if err := os.Chmod(socketPath, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot set socket permissions: %v\n", err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	fmt.Printf("GPG_AGENT_SOCKET=%s\n", socketPath)

	httpClient := &http.Client{Timeout: 30 * time.Second}

	// Flag to track expected shutdown
	shutdownCh := make(chan struct{})

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				// Check if this is an expected shutdown
				select {
				case <-shutdownCh:
					return
				default:
					// Unexpected error - log it
					fmt.Fprintf(os.Stderr, "error accepting connection: %v\n", err)
					return
				}
			}
			go handleConnection(conn, hostProxy, httpClient)
		}
	}()

	<-sigCh
	close(shutdownCh)
}

func handleConnection(conn net.Conn, hostProxy string, client *http.Client) {
	defer conn.Close()

	// GPG agent protocol (Assuan) is line-based
	// Read commands from client and forward to host proxy
	for {
		// Read up to maxMessageSize bytes
		// Assuan commands are typically small, line-based messages
		buf := make([]byte, maxMessageSize)
		n, err := conn.Read(buf)
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "error reading from client: %v\n", err)
			}
			return
		}

		if n == 0 {
			return
		}

		response, err := forwardToProxy(client, hostProxy, buf[:n])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error forwarding to proxy: %v\n", err)
			// Try to send an error response in Assuan format
			conn.Write([]byte("ERR 67108922 Host proxy error\n"))
			return
		}

		if _, err := conn.Write(response); err != nil {
			fmt.Fprintf(os.Stderr, "error writing response: %v\n", err)
			return
		}
	}
}

func forwardToProxy(client *http.Client, hostProxy string, msgData []byte) ([]byte, error) {
	req := gpgAgentRequest{Data: base64.StdEncoding.EncodeToString(msgData)}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := client.Post(hostProxy+"/gpg/agent", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to contact host proxy: %w", err)
	}
	defer resp.Body.Close()

	// Check HTTP status before attempting to decode JSON
	if resp.StatusCode >= 400 {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
		if err != nil {
			return nil, fmt.Errorf("host proxy returned HTTP %d (failed to read body: %v)", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("host proxy returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var agentResp gpgAgentResponse
	if err := json.NewDecoder(resp.Body).Decode(&agentResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if !agentResp.Success {
		return nil, fmt.Errorf("proxy error: %s", agentResp.Error)
	}

	// Defensive check: success response should have data
	if agentResp.Data == "" {
		return nil, fmt.Errorf("proxy returned success but no data")
	}

	responseData, err := base64.StdEncoding.DecodeString(agentResp.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode response data: %w", err)
	}

	return responseData, nil
}
