// callback-forwarder polls the host proxy for captured OAuth callback data and
// forwards it to the local HTTP server (Claude Code's callback listener).
//
// Usage:
//
//	callback-forwarder -session SESSION_ID -port PORT [-proxy URL] [-timeout SECONDS] [-poll SECONDS]
//
// Environment variables:
//
//	CLAWKER_HOST_PROXY: Host proxy URL (default: http://host.docker.internal:18374)
//	CALLBACK_SESSION: Session ID to poll for
//	CALLBACK_PORT: Local port to forward callback to
//	CB_FORWARDER_TIMEOUT: Timeout in seconds (default: 300)
//	CB_FORWARDER_POLL_INTERVAL: Poll interval in seconds (default: 2)
//	CB_FORWARDER_CLEANUP: Delete session after forwarding (default: true)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// CallbackData matches the CallbackData struct from the host proxy.
type CallbackData struct {
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Query      string            `json:"query"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	ReceivedAt string            `json:"received_at"`
}

// CallbackDataResponse matches the response from GET /callback/{session}/data.
type CallbackDataResponse struct {
	Received bool          `json:"received"`
	Callback *CallbackData `json:"callback,omitempty"`
	Error    string        `json:"error,omitempty"`
}

func main() {
	// Parse flags
	sessionID := flag.String("session", os.Getenv("CALLBACK_SESSION"), "Callback session ID")
	port := flag.Int("port", 0, "Local port to forward callback to")
	proxyURL := flag.String("proxy", os.Getenv("CLAWKER_HOST_PROXY"), "Host proxy URL")
	timeout := flag.Int("timeout", 300, "Timeout in seconds (default: 300)")
	pollInterval := flag.Int("poll", 2, "Poll interval in seconds (default: 2)")
	cleanup := flag.Bool("cleanup", true, "Delete session after forwarding (default: true)")
	verbose := flag.Bool("v", false, "Verbose output")
	flag.Parse()

	// Environment variable fallbacks for flags (CB_FORWARDER_ prefix to avoid collisions)
	if !flagWasSet("timeout") {
		if v := os.Getenv("CB_FORWARDER_TIMEOUT"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				*timeout = n
			} else {
				fmt.Fprintf(os.Stderr, "Warning: invalid CB_FORWARDER_TIMEOUT value %q, using default %d\n", v, *timeout)
			}
		}
	}
	if !flagWasSet("poll") {
		if v := os.Getenv("CB_FORWARDER_POLL_INTERVAL"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				*pollInterval = n
			} else {
				fmt.Fprintf(os.Stderr, "Warning: invalid CB_FORWARDER_POLL_INTERVAL value %q, using default %d\n", v, *pollInterval)
			}
		}
	}
	if !flagWasSet("cleanup") {
		if v := os.Getenv("CB_FORWARDER_CLEANUP"); v != "" {
			*cleanup = v == "true" || v == "1" || v == "yes"
		}
	}

	// Handle port from environment if not set via flag
	if *port == 0 {
		portEnv := os.Getenv("CALLBACK_PORT")
		if portEnv != "" {
			if _, err := fmt.Sscanf(portEnv, "%d", port); err != nil {
				fmt.Fprintf(os.Stderr, "Error: invalid CALLBACK_PORT value '%s': %v\n", portEnv, err)
				os.Exit(1)
			}
		}
	}

	// Validate required parameters
	if *sessionID == "" {
		fmt.Fprintln(os.Stderr, "Error: session ID required (-session or CALLBACK_SESSION)")
		os.Exit(1)
	}
	if *port == 0 {
		fmt.Fprintln(os.Stderr, "Error: port required (-port or CALLBACK_PORT)")
		os.Exit(1)
	}
	if *proxyURL == "" {
		// Default to host.docker.internal for Docker containers
		*proxyURL = "http://host.docker.internal:18374"
	}

	// Ensure proxyURL doesn't have trailing slash
	*proxyURL = strings.TrimSuffix(*proxyURL, "/")

	if *verbose {
		fmt.Fprintf(os.Stderr, "Waiting for OAuth callback...\n")
		fmt.Fprintf(os.Stderr, "  Session: %s\n", *sessionID)
		fmt.Fprintf(os.Stderr, "  Port: %d\n", *port)
		fmt.Fprintf(os.Stderr, "  Proxy: %s\n", *proxyURL)
		fmt.Fprintf(os.Stderr, "  Timeout: %ds\n", *timeout)
	}

	// Create HTTP client with reasonable timeout
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	escapedSession := url.PathEscape(*sessionID)
	dataURL := fmt.Sprintf("%s/callback/%s/data", *proxyURL, escapedSession)
	deleteURL := fmt.Sprintf("%s/callback/%s", *proxyURL, escapedSession)
	deadline := time.Now().Add(time.Duration(*timeout) * time.Second)

	// Track consecutive errors for user feedback
	consecutiveErrors := 0
	const maxSilentErrors = 3

	// Track time for periodic progress
	lastProgressAt := time.Now()
	const progressInterval = 30 * time.Second

	// Poll for callback data
	for time.Now().Before(deadline) {
		resp, err := client.Get(dataURL)
		if err != nil {
			consecutiveErrors++
			if *verbose {
				fmt.Fprintf(os.Stderr, "Poll error: %v\n", err)
			} else if consecutiveErrors == maxSilentErrors {
				fmt.Fprintln(os.Stderr, "Warning: multiple poll errors, retrying...")
			} else if consecutiveErrors > maxSilentErrors && time.Since(lastProgressAt) >= progressInterval {
				remaining := time.Until(deadline).Truncate(time.Second)
				fmt.Fprintf(os.Stderr, "Still waiting for callback (%s remaining, %d errors)...\n", remaining, consecutiveErrors)
				lastProgressAt = time.Now()
			}
			time.Sleep(time.Duration(*pollInterval) * time.Second)
			continue
		}
		consecutiveErrors = 0

		// Check status code first before decoding
		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			fmt.Fprintln(os.Stderr, "Error: session not found or expired")
			os.Exit(1)
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if *verbose {
				fmt.Fprintf(os.Stderr, "Unexpected status %d: %s\n", resp.StatusCode, string(body))
			} else {
				fmt.Fprintf(os.Stderr, "Warning: proxy returned status %d, retrying...\n", resp.StatusCode)
			}
			time.Sleep(time.Duration(*pollInterval) * time.Second)
			continue
		}

		var dataResp CallbackDataResponse
		if err := json.NewDecoder(resp.Body).Decode(&dataResp); err != nil {
			resp.Body.Close()
			consecutiveErrors++
			if *verbose {
				fmt.Fprintf(os.Stderr, "Decode error: %v\n", err)
			} else if consecutiveErrors == maxSilentErrors {
				fmt.Fprintln(os.Stderr, "Warning: multiple decode errors, retrying...")
			}
			time.Sleep(time.Duration(*pollInterval) * time.Second)
			continue
		}
		resp.Body.Close()

		// Check for server-side error in response
		if dataResp.Error != "" {
			fmt.Fprintf(os.Stderr, "Error from proxy: %s\n", dataResp.Error)
			os.Exit(1)
		}

		if !dataResp.Received {
			// No callback yet, keep polling
			time.Sleep(time.Duration(*pollInterval) * time.Second)
			continue
		}

		// Callback received! Forward it
		if *verbose {
			fmt.Fprintf(os.Stderr, "Callback received, forwarding to localhost:%d\n", *port)
		}

		forwardErr := forwardCallback(client, *port, dataResp.Callback)
		if forwardErr != nil {
			fmt.Fprintf(os.Stderr, "Error forwarding callback: %v\n", forwardErr)
		} else if *verbose {
			fmt.Fprintf(os.Stderr, "Callback forwarded successfully\n")
		}

		// Cleanup session
		if *cleanup {
			req, err := http.NewRequest(http.MethodDelete, deleteURL, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to create cleanup request: %v\n", err)
			} else {
				resp, err := client.Do(req)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to cleanup session: %v\n", err)
				} else {
					resp.Body.Close()
				}
			}
		}

		if forwardErr != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	fmt.Fprintln(os.Stderr, "Timeout waiting for OAuth callback")
	os.Exit(1)
}

// flagWasSet returns true if the named flag was explicitly passed on the command line.
func flagWasSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

// forwardCallback makes an HTTP request to the local port with the captured callback data.
func forwardCallback(client *http.Client, port int, data *CallbackData) error {
	if data == nil {
		return fmt.Errorf("no callback data")
	}

	// Build the local URL
	localURL := fmt.Sprintf("http://localhost:%d%s", port, data.Path)
	if data.Query != "" {
		localURL += "?" + data.Query
	}

	var body io.Reader
	if data.Body != "" {
		body = strings.NewReader(data.Body)
	}

	if data.Method == "" {
		return fmt.Errorf("callback data has empty HTTP method")
	}

	req, err := http.NewRequest(data.Method, localURL, body)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set captured headers
	for k, v := range data.Headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to forward request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if len(body) > 0 {
			return fmt.Errorf("local server returned status %d: %s", resp.StatusCode, string(body))
		}
		return fmt.Errorf("local server returned status %d", resp.StatusCode)
	}

	return nil
}
