package adminclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/schmitthub/clawker/internal/consts"
)

// readyPollInterval is the gap between /healthz samples while the CP boots.
const readyPollInterval = 250 * time.Millisecond

// readyProbeTimeout bounds one /healthz request.
const readyProbeTimeout = 2 * time.Second

// AwaitReady blocks while the CP on healthPort reports it is still booting
// ({"status":"not_ready"} on /healthz), so an admin RPC sent by a second CLI
// process during a cold start is not refused by the CP ready gate. It
// returns nil at once when the CP is ready, unhealthy, or unreachable: in
// those states waiting cannot help, and the RPC reports the real error.
// It returns an error when the CP is still booting after maxWait.
func AwaitReady(ctx context.Context, healthPort int, maxWait time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()
	url := fmt.Sprintf("http://"+consts.Localhost+":%d/healthz", healthPort)
	client := &http.Client{Timeout: readyProbeTimeout}
	for {
		booting := cpBooting(ctx, client, url)
		if ctx.Err() != nil {
			// The deadline fired mid-request while the CP was booting.
			return fmt.Errorf("control plane still starting up after %s: %w", maxWait, ctx.Err())
		}
		if !booting {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("control plane still starting up after %s: %w", maxWait, ctx.Err())
		case <-time.After(readyPollInterval):
		}
	}
}

// cpBooting reports whether one /healthz sample says not_ready. A failed
// request is not booting: the CP is down or unreachable.
func cpBooting(ctx context.Context, client *http.Client, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() {
		_ = resp.Body.Close() // Read-only response; a close error changes nothing.
	}()
	if resp.StatusCode != http.StatusServiceUnavailable {
		return false
	}
	var body struct {
		Status string `json:"status"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&body); decodeErr != nil {
		return false
	}
	return body.Status == "not_ready"
}
