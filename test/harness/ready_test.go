package harness

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConstants(t *testing.T) {
	// Verify timeout constants are reasonable
	assert.Equal(t, 60*time.Second, DefaultReadyTimeout)
	assert.Equal(t, 120*time.Second, E2EReadyTimeout)
	assert.Equal(t, 180*time.Second, CIReadyTimeout)
	assert.Equal(t, 10*time.Second, BypassCommandTimeout)

	// Verify path constants
	assert.Equal(t, "/var/run/clawker/ready", ReadyFilePath)
	assert.Equal(t, "[clawker] ready", ReadyLogPrefix)
	assert.Equal(t, "[clawker] error", ErrorLogPrefix)
}

func TestGetReadyTimeout(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		expected time.Duration
	}{
		{
			name:     "default when no env set",
			envVars:  map[string]string{},
			expected: DefaultReadyTimeout,
		},
		{
			name: "custom timeout from env",
			envVars: map[string]string{
				"CLAWKER_READY_TIMEOUT": "30",
			},
			expected: 30 * time.Second,
		},
		{
			name: "CI timeout when CI is true",
			envVars: map[string]string{
				"CI": "true",
			},
			expected: CIReadyTimeout,
		},
		{
			name: "CI timeout when GITHUB_ACTIONS is true",
			envVars: map[string]string{
				"GITHUB_ACTIONS": "true",
			},
			expected: CIReadyTimeout,
		},
		{
			name: "custom overrides CI",
			envVars: map[string]string{
				"CLAWKER_READY_TIMEOUT": "45",
				"CI":                    "true",
			},
			expected: 45 * time.Second,
		},
		{
			name: "invalid env value falls back to default",
			envVars: map[string]string{
				"CLAWKER_READY_TIMEOUT": "invalid",
			},
			expected: DefaultReadyTimeout,
		},
		{
			name: "zero value falls back to default",
			envVars: map[string]string{
				"CLAWKER_READY_TIMEOUT": "0",
			},
			expected: DefaultReadyTimeout,
		},
		{
			name: "negative value falls back to default",
			envVars: map[string]string{
				"CLAWKER_READY_TIMEOUT": "-5",
			},
			expected: DefaultReadyTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and clear relevant env vars
			savedVars := make(map[string]string)
			keysToRestore := []string{"CLAWKER_READY_TIMEOUT", "CI", "GITHUB_ACTIONS"}
			for _, key := range keysToRestore {
				savedVars[key] = os.Getenv(key)
				os.Unsetenv(key)
			}

			// Set test env vars
			for k, v := range tt.envVars {
				os.Setenv(k, v)
			}

			// Run test
			result := GetReadyTimeout()
			assert.Equal(t, tt.expected, result)

			// Restore env vars
			for _, key := range keysToRestore {
				if val, existed := savedVars[key]; existed && val != "" {
					os.Setenv(key, val)
				} else {
					os.Unsetenv(key)
				}
			}
		})
	}
}

func TestCheckForErrorPattern(t *testing.T) {
	tests := []struct {
		name        string
		logs        string
		wantFound   bool
		wantMessage string
	}{
		{
			name:      "no error pattern",
			logs:      "[clawker] ready ts=1234567890 agent=test\nStarting...\nDone",
			wantFound: false,
		},
		{
			name:        "error pattern with msg",
			logs:        "[clawker] error component=firewall msg=initialization failed\nOther log",
			wantFound:   true,
			wantMessage: "initialization failed",
		},
		{
			name:        "error pattern without msg",
			logs:        "[clawker] error some error happened",
			wantFound:   true,
			wantMessage: " some error happened",
		},
		{
			name:      "empty logs",
			logs:      "",
			wantFound: false,
		},
		{
			name:        "error in middle of logs",
			logs:        "Starting up...\nLoading config...\n[clawker] error component=config msg=invalid yaml\nShutting down",
			wantFound:   true,
			wantMessage: "invalid yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found, msg := CheckForErrorPattern(tt.logs)
			assert.Equal(t, tt.wantFound, found)
			if tt.wantFound {
				assert.Contains(t, msg, tt.wantMessage)
			}
		})
	}
}

func TestParseReadyFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    *ReadyFileContent
		wantErr bool
	}{
		{
			name:    "valid content",
			content: "ts=1234567890 pid=12345",
			want: &ReadyFileContent{
				Timestamp: 1234567890,
				PID:       12345,
			},
			wantErr: false,
		},
		{
			name:    "reversed order",
			content: "pid=99 ts=9876543210",
			want: &ReadyFileContent{
				Timestamp: 9876543210,
				PID:       99,
			},
			wantErr: false,
		},
		{
			name:    "only timestamp",
			content: "ts=1111111111",
			want: &ReadyFileContent{
				Timestamp: 1111111111,
				PID:       0,
			},
			wantErr: false,
		},
		{
			name:    "missing timestamp",
			content: "pid=123",
			wantErr: true,
		},
		{
			name:    "invalid timestamp",
			content: "ts=invalid pid=123",
			wantErr: true,
		},
		{
			name:    "invalid pid",
			content: "ts=123 pid=invalid",
			wantErr: true,
		},
		{
			name:    "empty content",
			content: "",
			wantErr: true,
		},
		{
			name:    "content with extra whitespace",
			content: "  ts=1234567890   pid=12345  ",
			want: &ReadyFileContent{
				Timestamp: 1234567890,
				PID:       12345,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseReadyFile(tt.content)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want.Timestamp, result.Timestamp)
			assert.Equal(t, tt.want.PID, result.PID)
		})
	}
}
