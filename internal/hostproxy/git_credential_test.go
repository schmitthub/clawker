package hostproxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleGitCredential(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantError  string
	}{
		{
			name:       "invalid JSON",
			body:       "not json",
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid JSON request body",
		},
		{
			name:       "missing action",
			body:       `{"protocol": "https", "host": "github.com"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "action must be 'get', 'store', or 'erase'",
		},
		{
			name:       "empty action",
			body:       `{"action": "", "protocol": "https", "host": "github.com"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "action must be 'get', 'store', or 'erase'",
		},
		{
			name:       "invalid action",
			body:       `{"action": "invalid", "protocol": "https", "host": "github.com"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "action must be 'get', 'store', or 'erase'",
		},
		{
			name:       "missing protocol",
			body:       `{"action": "get", "host": "github.com"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "protocol is required",
		},
		{
			name:       "missing host",
			body:       `{"action": "get", "protocol": "https"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "host is required",
		},
		{
			name:       "empty protocol",
			body:       `{"action": "get", "protocol": "", "host": "github.com"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "protocol is required",
		},
		{
			name:       "empty host",
			body:       `{"action": "get", "protocol": "https", "host": ""}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "host is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{}
			req := httptest.NewRequest(http.MethodPost, "/git/credential", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			s.handleGitCredential(w, req)

			resp := w.Result()
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, resp.StatusCode)
			}

			var result gitCredentialResponse
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if result.Success {
				t.Error("expected success to be false")
			}

			if result.Error != tt.wantError {
				t.Errorf("expected error %q, got %q", tt.wantError, result.Error)
			}
		})
	}
}

func TestHandleGitCredentialBodySizeLimit(t *testing.T) {
	s := &Server{}

	// Create a body larger than maxRequestBodySize (1MB)
	largeBody := make([]byte, maxRequestBodySize+1)
	for i := range largeBody {
		largeBody[i] = 'a'
	}

	req := httptest.NewRequest(http.MethodPost, "/git/credential", bytes.NewReader(largeBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleGitCredential(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400 for oversized body, got %d", resp.StatusCode)
	}

	var result gitCredentialResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result.Success {
		t.Error("expected success to be false")
	}
}

func TestFormatGitCredentialInput(t *testing.T) {
	tests := []struct {
		name     string
		req      gitCredentialRequest
		expected string
	}{
		{
			name: "minimal request",
			req: gitCredentialRequest{
				Action:   "get",
				Protocol: "https",
				Host:     "github.com",
			},
			expected: "protocol=https\nhost=github.com\n\n",
		},
		{
			name: "with path",
			req: gitCredentialRequest{
				Action:   "get",
				Protocol: "https",
				Host:     "github.com",
				Path:     "user/repo.git",
			},
			expected: "protocol=https\nhost=github.com\npath=user/repo.git\n\n",
		},
		{
			name: "with username",
			req: gitCredentialRequest{
				Action:   "get",
				Protocol: "https",
				Host:     "github.com",
				Username: "testuser",
			},
			expected: "protocol=https\nhost=github.com\nusername=testuser\n\n",
		},
		{
			name: "with password (for store)",
			req: gitCredentialRequest{
				Action:   "store",
				Protocol: "https",
				Host:     "github.com",
				Username: "testuser",
				Password: "secretpass",
			},
			expected: "protocol=https\nhost=github.com\nusername=testuser\npassword=secretpass\n\n",
		},
		{
			name: "full request",
			req: gitCredentialRequest{
				Action:   "store",
				Protocol: "https",
				Host:     "github.com",
				Path:     "user/repo.git",
				Username: "testuser",
				Password: "secretpass",
			},
			expected: "protocol=https\nhost=github.com\npath=user/repo.git\nusername=testuser\npassword=secretpass\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatGitCredentialInput(tt.req)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestParseGitCredentialOutput(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected gitCredentials
	}{
		{
			name:   "empty output",
			output: "",
			expected: gitCredentials{
				Protocol: "",
				Host:     "",
				Username: "",
				Password: "",
			},
		},
		{
			name:   "minimal credentials",
			output: "protocol=https\nhost=github.com\n",
			expected: gitCredentials{
				Protocol: "https",
				Host:     "github.com",
				Username: "",
				Password: "",
			},
		},
		{
			name:   "full credentials",
			output: "protocol=https\nhost=github.com\nusername=testuser\npassword=secretpass\n",
			expected: gitCredentials{
				Protocol: "https",
				Host:     "github.com",
				Username: "testuser",
				Password: "secretpass",
			},
		},
		{
			name:   "with trailing blank line",
			output: "protocol=https\nhost=github.com\nusername=testuser\npassword=secretpass\n\n",
			expected: gitCredentials{
				Protocol: "https",
				Host:     "github.com",
				Username: "testuser",
				Password: "secretpass",
			},
		},
		{
			name:   "ignores unknown keys",
			output: "protocol=https\nhost=github.com\nunknown=value\nusername=testuser\n",
			expected: gitCredentials{
				Protocol: "https",
				Host:     "github.com",
				Username: "testuser",
				Password: "",
			},
		},
		{
			name:   "ignores malformed lines",
			output: "protocol=https\nhost=github.com\nmalformed line without equals\nusername=testuser\n",
			expected: gitCredentials{
				Protocol: "https",
				Host:     "github.com",
				Username: "testuser",
				Password: "",
			},
		},
		{
			name:   "handles value with equals sign",
			output: "protocol=https\nhost=github.com\npassword=secret=with=equals\n",
			expected: gitCredentials{
				Protocol: "https",
				Host:     "github.com",
				Password: "secret=with=equals",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseGitCredentialOutput(tt.output)

			if result.Protocol != tt.expected.Protocol {
				t.Errorf("Protocol: expected %q, got %q", tt.expected.Protocol, result.Protocol)
			}
			if result.Host != tt.expected.Host {
				t.Errorf("Host: expected %q, got %q", tt.expected.Host, result.Host)
			}
			if result.Username != tt.expected.Username {
				t.Errorf("Username: expected %q, got %q", tt.expected.Username, result.Username)
			}
			if result.Password != tt.expected.Password {
				t.Errorf("Password: expected %q, got %q", tt.expected.Password, result.Password)
			}
		})
	}
}
