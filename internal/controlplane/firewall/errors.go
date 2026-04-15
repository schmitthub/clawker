package firewall

import (
	"errors"
	"fmt"
	"time"
)

// Sentinel errors for firewall stack health check failures.
var (
	ErrEnvoyUnhealthy   = errors.New("envoy not healthy")
	ErrCoreDNSUnhealthy = errors.New("coredns not healthy")
	ErrCPUnhealthy      = errors.New("clawker-controlplane not healthy")
)

// HealthTimeoutError is returned when a firewall stack health wait exceeds
// its deadline. Err wraps one or more of the sentinel errors above.
type HealthTimeoutError struct {
	Timeout time.Duration
	Err     error
}

func (e *HealthTimeoutError) Error() string {
	return fmt.Sprintf("%v after %s — this can happen during first run when firewall container images are being pulled, or may indicate a bug. Run 'docker ps -a --filter label=dev.clawker.purpose=firewall' to check container status, or try the command again", e.Err, e.Timeout)
}

func (e *HealthTimeoutError) Unwrap() error { return e.Err }
