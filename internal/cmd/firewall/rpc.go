package firewall

import (
	"context"
	"fmt"
	"time"

	"github.com/schmitthub/clawker/internal/iostreams"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
)

// rpcTimeout bounds every AdminService call the firewall CLI makes.
// The CP queue serializes work so a single RPC can wait behind other
// queued actions + its own reconcile (worst case a few seconds). 15s
// gives comfortable headroom while keeping a stuck CP from hanging the
// CLI indefinitely — the prior behavior was unbounded wait.
const rpcTimeout = 15 * time.Second

// callWithSpinner runs fn under a stderr spinner with label and the
// firewall CLI's fixed 15s RPC timeout, returning the typed result or
// fn's error unchanged. The spinner auto-disables in non-TTY contexts
// (pipes, CI, scripts) so machine-readable output is never polluted by
// cursor escapes. Keeps each command's run function focused on its
// per-RPC wire handling.
func callWithSpinner[T any](ctx context.Context, ios *iostreams.IOStreams, label string, fn func(context.Context) (T, error)) (T, error) {
	var (
		result  T
		callErr error
	)
	_ = ios.RunWithSpinner(label, func() error {
		rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
		defer cancel()
		result, callErr = fn(rpcCtx)
		return callErr
	})
	return result, callErr
}

// remediationLines extracts per-sentinel remediation strings from a
// gRPC error's errdetails.ErrorInfo entries. The CLI prints one line
// per matched Reason so a reconcile cycle that fails multiple sub-steps
// (e.g. envoy_restart + coredns_restart) surfaces both hints. Returns
// nil for nil err; returns a single-entry slice with the status message
// when no typed details are attached so the CLI never silently swallows
// a failure.
func remediationLines(err error) []string {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return []string{err.Error()}
	}
	var out []string
	for _, d := range st.Details() {
		info, ok := d.(*errdetails.ErrorInfo)
		if !ok {
			continue
		}
		if hint := remediationForReason(info.GetReason()); hint != "" {
			out = append(out, hint)
		}
	}
	if len(out) == 0 {
		out = append(out, st.Message())
	}
	return out
}

// remediationForReason maps a wire Reason to the human-facing next-step
// hint. See internal/controlplane/firewall/errors.go for the catalog.
// Unknown reasons return "" so the caller can fall back to the status
// message rather than printing a cryptic Reason constant.
func remediationForReason(reason string) string {
	switch reason {
	case "CP_NOT_RUNNING":
		return "control plane is not running — run `clawker controlplane up`"
	case "QUEUE_CLOSED":
		return "control plane is shutting down, retry in a moment"
	case "FIREWALL_NOT_INITIALIZED":
		return "firewall is not running — run `clawker firewall up`"
	case "CONTAINER_GONE":
		return "target container no longer exists"
	case "RULE_INVALID":
		return "rule validation failed — check domain syntax, proto, and port"
	case "RULE_NOT_FOUND":
		return "no matching rule — run `clawker firewall list` to see current rules"
	case "RULE_STORE_WRITE":
		return "rule change was not persisted — state is unchanged, safe to retry"
	case "CERT_REGEN":
		return "CA rotation partially completed — new CA material may exist on disk but the running stack was not reloaded; inspect the firewall cert dir and rerun `clawker firewall rotate-ca` or `clawker firewall reload` after resolving the underlying issue"
	case "STACK_PROBE":
		return "cannot determine firewall stack state — check Docker daemon health"
	case "CONFIG_REGEN":
		return "stack config regeneration failed — rule is persisted; stack was NOT restarted"
	case "ENVOY_RESTART":
		return "Envoy restart failed — run `clawker container logs clawker-envoy`"
	case "COREDNS_RESTART":
		return "CoreDNS restart failed — run `clawker container logs clawker-coredns`"
	case "STACK_UNHEALTHY":
		return "firewall containers started but are not healthy — inspect: `clawker firewall status`"
	case "ROUTE_SYNC":
		return "BPF route map sync failed — stack is running with potentially stale routes; rerun `clawker firewall reload`"
	default:
		return ""
	}
}

// wrapRPCError formats a gRPC error with header + one remediation line
// per matched errdetails.ErrorInfo so the caller can `return` a clean
// error that carries next-step guidance already baked in. Fallback is
// the status message when no typed details are attached.
func wrapRPCError(header string, err error) error {
	if err == nil {
		return nil
	}
	hints := remediationLines(err)
	if len(hints) == 0 {
		return fmt.Errorf("%s: %w", header, err)
	}
	msg := header
	for _, h := range hints {
		msg += "\n  - " + h
	}
	return fmt.Errorf("%s: %w", msg, err)
}

// printStackRestartedNote prints a one-line info note about the
// stack_restarted field of a rule-CRUD / reload / rotate-CA response.
// When stack_restarted is false the RPC still succeeded (err==nil); the
// note explains that the on-disk change will take effect on next
// `firewall up`. Does nothing when restarted is true — the caller's
// default success line already said the change was applied live.
func printStackRestartedNote(ios *iostreams.IOStreams, restarted bool, what string) {
	if restarted {
		return
	}
	cs := ios.ColorScheme()
	fmt.Fprintf(ios.ErrOut, "%s %s; firewall is not running, will take effect on next `clawker firewall up`\n",
		cs.InfoIcon(), what)
}
