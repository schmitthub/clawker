// ssh-agent-proxy forwards SSH agent requests to the clawker host proxy.
// This allows containers to use the host's SSH agent even when direct socket
// mounting has permission issues (e.g., macOS Docker Desktop).
//
// Build: go build -o ssh-agent-proxy ssh-agent-proxy.go
// Usage: ssh-agent-proxy (runs in background, outputs SSH_AUTH_SOCK path)
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
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

const maxMessageSize = 64 * 1024

type sshAgentRequest struct {
	Data string `json:"data"`
}

type sshAgentResponse struct {
	Success bool   `json:"success"`
	Data    string `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
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

	sshDir := filepath.Join(homeDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot create .ssh directory: %v\n", err)
		os.Exit(1)
	}

	socketPath := filepath.Join(sshDir, "agent.sock")

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

	if err := os.Chmod(socketPath, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot set socket permissions: %v\n", err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	fmt.Printf("SSH_AUTH_SOCK=%s\n", socketPath)

	httpClient := &http.Client{Timeout: 30 * time.Second}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleConnection(conn, hostProxy, httpClient)
		}
	}()

	<-sigCh
}

func handleConnection(conn net.Conn, hostProxy string, client *http.Client) {
	defer conn.Close()

	for {
		lenBuf := make([]byte, 4)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "error reading message length: %v\n", err)
			}
			return
		}

		msgLen := binary.BigEndian.Uint32(lenBuf)
		if msgLen > maxMessageSize {
			fmt.Fprintf(os.Stderr, "message too large: %d bytes\n", msgLen)
			return
		}

		msgBuf := make([]byte, 4+msgLen)
		copy(msgBuf[:4], lenBuf)
		if _, err := io.ReadFull(conn, msgBuf[4:]); err != nil {
			fmt.Fprintf(os.Stderr, "error reading message body: %v\n", err)
			return
		}

		response, err := forwardToProxy(client, hostProxy, msgBuf)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error forwarding to proxy: %v\n", err)
			return
		}

		if _, err := conn.Write(response); err != nil {
			fmt.Fprintf(os.Stderr, "error writing response: %v\n", err)
			return
		}
	}
}

func forwardToProxy(client *http.Client, hostProxy string, msgData []byte) ([]byte, error) {
	req := sshAgentRequest{Data: base64.StdEncoding.EncodeToString(msgData)}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := client.Post(hostProxy+"/ssh/agent", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to contact host proxy: %w", err)
	}
	defer resp.Body.Close()

	var agentResp sshAgentResponse
	if err := json.NewDecoder(resp.Body).Decode(&agentResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if !agentResp.Success {
		return nil, fmt.Errorf("proxy error: %s", agentResp.Error)
	}

	responseData, err := base64.StdEncoding.DecodeString(agentResp.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode response data: %w", err)
	}

	return responseData, nil
}
