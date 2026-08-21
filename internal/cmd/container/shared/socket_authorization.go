package shared

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/db"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/internal/text"
)

const (
	dockerSocketSetting = "security.docker_socket"

	socketAnswerAlways = "always"
	socketAnswerYes    = "yes"
	socketAnswerNo     = "no"
	socketAnswerNever  = "never"

	eventOptionalSocketSkipped = "optional_socket_bridge_skipped"
	eventStoredSocketDenied    = "stored_socket_bridge_denied"
)

type socketCandidate struct {
	declaration config.HarnessSocket
	hostPath    string
	identity    socketbridge.ListenerIdentity
}

func authorizeSocketBridges(
	ctx context.Context,
	container string,
	harness RuntimeHarness,
	cmdOpts CommandOpts,
	log *logger.Logger,
) ([]socketbridge.BridgedSocket, error) {
	if log == nil {
		log = logger.Nop()
	}
	if len(harness.Sockets) == 0 {
		return nil, nil
	}
	if !harness.HasContainerLabel {
		return nil, fmt.Errorf(
			"container %s cannot request host sockets without the %s label; rebuild the container",
			container,
			consts.LabelHarness,
		)
	}

	candidates, resolveErr := resolveSocketCandidates(harness.Name, harness.Sockets, cmdOpts, log)
	if resolveErr != nil {
		return nil, resolveErr
	}
	if harness.Provenance.Tier == bundle.TierFloor {
		return candidateBridges(candidates), nil
	}

	principal, principalErr := cmdutil.ResolveHostPath(harness.Provenance.Dir)
	if principalErr != nil {
		return nil, fmt.Errorf(
			"authorize socket bridges for harness %q: resolve principal: %w",
			harness.Name,
			principalErr,
		)
	}
	store, storeErr := openSocketGrantStore(cmdOpts)
	if storeErr != nil {
		return nil, storeErr
	}
	if pruneErr := store.PruneHarnessSockets(ctx, principal, candidateHostPaths(candidates)); pruneErr != nil {
		return nil, fmt.Errorf("authorize socket bridges: prune harness grants: %w", pruneErr)
	}
	return authorizeSocketCandidates(ctx, harness.Name, principal, candidates, cmdOpts, store, log)
}

//nolint:ireturn // Authorization uses the store seam.
func openSocketGrantStore(
	cmdOpts CommandOpts,
) (db.SocketGrantStore, error) {
	if cmdOpts.SocketGrants == nil {
		return nil, errors.New("authorize socket bridges: socket grant store provider is nil")
	}
	store, err := cmdOpts.SocketGrants()
	if err != nil {
		return nil, fmt.Errorf("authorize socket bridges: open grant store: %w", err)
	}
	if store == nil {
		return nil, errors.New("authorize socket bridges: socket grant store is nil")
	}
	return store, nil
}

func candidateHostPaths(candidates []socketCandidate) []string {
	hostPaths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		hostPaths = append(hostPaths, candidate.hostPath)
	}
	return hostPaths
}

func authorizeSocketCandidates(
	ctx context.Context,
	harnessName,
	principal string,
	candidates []socketCandidate,
	cmdOpts CommandOpts,
	store db.SocketGrantStore,
	log *logger.Logger,
) ([]socketbridge.BridgedSocket, error) {
	bridges := make([]socketbridge.BridgedSocket, 0, len(candidates))
	for _, candidate := range candidates {
		bridge, active, authorizeErr := authorizeSocketCandidate(
			ctx,
			harnessName,
			principal,
			candidate,
			cmdOpts,
			store,
			log,
		)
		if authorizeErr != nil {
			return nil, authorizeErr
		}
		if active {
			bridges = append(bridges, bridge)
		}
	}
	return bridges, nil
}

func resolveSocketCandidates(
	harnessName string,
	declarations []config.HarnessSocket,
	cmdOpts CommandOpts,
	log *logger.Logger,
) ([]socketCandidate, error) {
	candidates := make([]socketCandidate, 0, len(declarations))
	for _, declaration := range declarations {
		candidate, include, resolveErr := resolveSocketCandidate(harnessName, declaration, cmdOpts, log)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if include {
			candidates = append(candidates, candidate)
		}
	}
	return candidates, nil
}

func resolveSocketCandidate(
	harnessName string,
	declaration config.HarnessSocket,
	cmdOpts CommandOpts,
	log *logger.Logger,
) (socketCandidate, bool, error) {
	var empty socketCandidate
	hostPath, resolveErr := cmdutil.ResolveHostPath(declaration.Source)
	if resolveErr != nil {
		if declaration.Optional {
			noticeErr := reportOptionalSocketSkip(
				cmdOpts, log, harnessName, declaration.Target, "host path did not resolve", resolveErr,
			)
			return empty, false, noticeErr
		}
		return empty, false, fmt.Errorf(
			"required socket bridge %s for harness %q: resolve host path: %w",
			declaration.Target,
			harnessName,
			resolveErr,
		)
	}
	if bannedPath, banned := bannedSocketPath(hostPath); banned {
		return empty, false, bannedSocketError(bannedPath)
	}
	identity, identityErr := socketbridge.ReadListenerIdentity(hostPath)
	if identityErr != nil {
		if declaration.Optional {
			noticeErr := reportOptionalSocketSkip(
				cmdOpts, log, harnessName, declaration.Target, "listener probe failed", identityErr,
			)
			return empty, false, noticeErr
		}
		return empty, false, fmt.Errorf(
			"required socket bridge %s for harness %q: probe host listener %s: %w",
			declaration.Target,
			harnessName,
			hostPath,
			identityErr,
		)
	}
	return socketCandidate{declaration: declaration, hostPath: hostPath, identity: identity}, true, nil
}

func bannedSocketError(path string) error {
	if path == consts.DockerSocketPath {
		return fmt.Errorf("host socket %s is banned; use %s for Docker daemon access", path, dockerSocketSetting)
	}
	return fmt.Errorf("host socket %s is banned", path)
}

func authorizeSocketCandidate(
	ctx context.Context,
	harnessName string,
	principal string,
	candidate socketCandidate,
	cmdOpts CommandOpts,
	store db.SocketGrantStore,
	log *logger.Logger,
) (socketbridge.BridgedSocket, bool, error) {
	grant, lookupErr := store.LookupSocketGrant(ctx, principal, candidate.hostPath)
	if lookupErr != nil && !errors.Is(lookupErr, db.ErrGrantNotFound) {
		return emptyBridgedSocket(), false, fmt.Errorf(
			"lookup socket grant for %s: %w",
			candidate.hostPath,
			lookupErr,
		)
	}
	if lookupErr == nil && grant == nil {
		return emptyBridgedSocket(), false, fmt.Errorf(
			"lookup socket grant for %s returned no grant",
			candidate.hostPath,
		)
	}
	if lookupErr == nil {
		return authorizeStoredSocketGrant(ctx, harnessName, principal, candidate, grant, cmdOpts, store, log)
	}

	if cmdOpts.ApproveGrants {
		return approveSocketCandidate(ctx, harnessName, principal, candidate, cmdOpts, store)
	}
	return authorizePromptedSocket(ctx, harnessName, principal, candidate, nil, cmdOpts, store, log)
}

func authorizeStoredSocketGrant(
	ctx context.Context,
	harnessName,
	principal string,
	candidate socketCandidate,
	grant *db.SocketGrant,
	cmdOpts CommandOpts,
	store db.SocketGrantStore,
	log *logger.Logger,
) (socketbridge.BridgedSocket, bool, error) {
	switch grant.Status {
	case db.GrantAllow:
		if listenerIdentityMatches(grant.Identity, candidate.identity) {
			return candidate.bridge(), true, nil
		}
		return authorizePromptedSocket(ctx, harnessName, principal, candidate, grant, cmdOpts, store, log)
	case db.GrantDeny:
		if candidate.declaration.Optional {
			logStoredSocketDenial(log, harnessName, candidate, grant.ID)
			return emptyBridgedSocket(), false, nil
		}
		return emptyBridgedSocket(), false, storedDenyError(candidate.hostPath, grant.ID)
	default:
		return emptyBridgedSocket(), false, fmt.Errorf(
			"socket grant %d has unknown status %q",
			grant.ID,
			grant.Status,
		)
	}
}

func logStoredSocketDenial(log *logger.Logger, harnessName string, candidate socketCandidate, grantID int64) {
	log.Info().
		Str("event", eventStoredSocketDenied).
		Str("harness", harnessName).
		Str("host_path", candidate.hostPath).
		Str("target", candidate.declaration.Target).
		Int64("grant_id", grantID).
		Msg("stored socket denial applied")
}

func approveSocketCandidate(
	ctx context.Context,
	harnessName,
	principal string,
	candidate socketCandidate,
	cmdOpts CommandOpts,
	store db.SocketGrantStore,
) (socketbridge.BridgedSocket, bool, error) {
	grantErr := store.GrantSocket(
		ctx,
		principal,
		harnessName,
		candidate.hostPath,
		candidate.declaration,
		candidate.identity,
	)
	if grantErr != nil {
		return emptyBridgedSocket(), false, fmt.Errorf(
			"store socket grant for %s: %w",
			candidate.hostPath,
			grantErr,
		)
	}
	if printErr := printSocketApproval(cmdOpts, candidate.hostPath); printErr != nil {
		return emptyBridgedSocket(), false, printErr
	}
	return candidate.bridge(), true, nil
}

func authorizePromptedSocket(
	ctx context.Context,
	harnessName string,
	principal string,
	candidate socketCandidate,
	previous *db.SocketGrant,
	cmdOpts CommandOpts,
	store db.SocketGrantStore,
	log *logger.Logger,
) (socketbridge.BridgedSocket, bool, error) {
	if !cmdOpts.IOStreams.CanPrompt() {
		return authorizeNonInteractiveSocket(harnessName, candidate, previous, cmdOpts, log)
	}

	answer, promptErr := promptForSocketGrant(cmdOpts, harnessName, principal, candidate, previous)
	if promptErr != nil {
		return emptyBridgedSocket(), false, promptErr
	}
	switch answer {
	case socketAnswerAlways:
		return approveSocketCandidate(ctx, harnessName, principal, candidate, cmdOpts, store)
	case socketAnswerYes:
		return candidate.bridge(), true, nil
	case socketAnswerNo:
		return declineSocketCandidate(harnessName, candidate, cmdOpts, log)
	case socketAnswerNever:
		return denySocketCandidate(ctx, harnessName, principal, candidate, previous, store)
	default:
		return emptyBridgedSocket(), false, fmt.Errorf("unknown socket authorization answer %q", answer)
	}
}

func authorizeNonInteractiveSocket(
	harnessName string,
	candidate socketCandidate,
	previous *db.SocketGrant,
	cmdOpts CommandOpts,
	log *logger.Logger,
) (socketbridge.BridgedSocket, bool, error) {
	if candidate.declaration.Optional {
		noticeErr := reportOptionalSocketSkip(
			cmdOpts,
			log,
			harnessName,
			candidate.declaration.Target,
			"approval requires an interactive start",
			nil,
		)
		return emptyBridgedSocket(), false, noticeErr
	}
	if previous != nil {
		return emptyBridgedSocket(), false, fmt.Errorf(
			"listener identity for socket bridge %s changed; run an interactive start to approve the current listener",
			candidate.hostPath,
		)
	}
	return emptyBridgedSocket(), false, missingGrantError(candidate.hostPath)
}

func declineSocketCandidate(
	harnessName string,
	candidate socketCandidate,
	cmdOpts CommandOpts,
	log *logger.Logger,
) (socketbridge.BridgedSocket, bool, error) {
	if !candidate.declaration.Optional {
		return emptyBridgedSocket(), false, missingGrantError(candidate.hostPath)
	}
	noticeErr := reportOptionalSocketSkip(
		cmdOpts,
		log,
		harnessName,
		candidate.declaration.Target,
		"approval was declined",
		nil,
	)
	return emptyBridgedSocket(), false, noticeErr
}

func denySocketCandidate(
	ctx context.Context,
	harnessName,
	principal string,
	candidate socketCandidate,
	previous *db.SocketGrant,
	store db.SocketGrantStore,
) (socketbridge.BridgedSocket, bool, error) {
	denyErr := store.DenySocket(
		ctx,
		principal,
		harnessName,
		candidate.hostPath,
		candidate.declaration,
		candidate.identity,
	)
	if denyErr != nil {
		return emptyBridgedSocket(), false, fmt.Errorf(
			"store socket denial for %s: %w",
			candidate.hostPath,
			denyErr,
		)
	}
	if candidate.declaration.Optional {
		return emptyBridgedSocket(), false, nil
	}
	if previous != nil {
		return emptyBridgedSocket(), false, storedDenyError(candidate.hostPath, previous.ID)
	}
	stored, lookupErr := store.LookupSocketGrant(ctx, principal, candidate.hostPath)
	if lookupErr != nil {
		return emptyBridgedSocket(), false, fmt.Errorf(
			"read stored socket denial for %s: %w",
			candidate.hostPath,
			lookupErr,
		)
	}
	if stored == nil {
		return emptyBridgedSocket(), false, fmt.Errorf(
			"stored socket denial for %s was not found",
			candidate.hostPath,
		)
	}
	return emptyBridgedSocket(), false, storedDenyError(candidate.hostPath, stored.ID)
}

func emptyBridgedSocket() socketbridge.BridgedSocket {
	var socket socketbridge.BridgedSocket
	return socket
}

func promptForSocketGrant(
	cmdOpts CommandOpts,
	harnessName string,
	principal string,
	candidate socketCandidate,
	previous *db.SocketGrant,
) (string, error) {
	ios := cmdOpts.IOStreams
	if _, err := fmt.Fprintf(
		ios.ErrOut,
		"Harness %q at %s requires access to a host socket.\n  Socket:  %s\n  Mounted: %s\n",
		harnessName,
		principal,
		candidate.hostPath,
		candidate.declaration.Target,
	); err != nil {
		return "", fmt.Errorf("write socket authorization details: %w", err)
	}
	if previous != nil {
		if _, err := fmt.Fprintf(
			ios.ErrOut,
			"  Previously approved listener: %s\n  Current listener:             %s\n",
			formatListenerIdentity(previous.Identity),
			formatListenerIdentity(candidate.identity),
		); err != nil {
			return "", fmt.Errorf("write changed listener identity: %w", err)
		}
	} else if _, err := fmt.Fprintf(ios.ErrOut, "  Listener: %s\n", formatListenerIdentity(candidate.identity)); err != nil {
		return "", fmt.Errorf("write socket listener identity: %w", err)
	}
	optionalNotice := ""
	if candidate.declaration.Optional {
		optionalNotice = "\nThis socket is optional. The harness can start without it."
	}
	if _, err := fmt.Fprintf(
		ios.ErrOut,
		"  Purpose: %s\nTraffic on this bridge bypasses the egress firewall.%s\n",
		text.SanitizeSingleLine(candidate.declaration.Purpose),
		optionalNotice,
	); err != nil {
		return "", fmt.Errorf("write socket authorization warning: %w", err)
	}
	prompt, providerErr := socketGrantPrompter(cmdOpts)
	if providerErr != nil {
		return "", providerErr
	}
	answer, err := prompt.StringUntilValid(prompter.PromptConfig{
		Message:  "Allow this socket bridge? [a]lways / [y]es / [n]o / ne[v]er",
		Default:  "",
		Required: true,
		Validator: func(value string) error {
			if normalizeSocketAnswer(value) == "" {
				return errors.New("enter always, yes, no, or never")
			}
			return nil
		},
	})
	if err != nil {
		return "", fmt.Errorf("read socket authorization answer: %w", err)
	}
	return normalizeSocketAnswer(answer), nil
}

func socketGrantPrompter(cmdOpts CommandOpts) (*prompter.Prompter, error) {
	if cmdOpts.Prompter == nil {
		return nil, errors.New("read socket authorization answer: prompter provider is nil")
	}
	prompt := cmdOpts.Prompter()
	if prompt == nil {
		return nil, errors.New("read socket authorization answer: prompter is nil")
	}
	return prompt, nil
}

func normalizeSocketAnswer(answer string) string {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "a", socketAnswerAlways:
		return socketAnswerAlways
	case "y", socketAnswerYes:
		return socketAnswerYes
	case "n", socketAnswerNo:
		return socketAnswerNo
	case "v", socketAnswerNever:
		return socketAnswerNever
	default:
		return ""
	}
}

func reportOptionalSocketSkip(
	cmdOpts CommandOpts,
	log *logger.Logger,
	harnessName string,
	target string,
	reason string,
	cause error,
) error {
	event := log.Warn().
		Str("event", eventOptionalSocketSkipped).
		Str("harness", harnessName).
		Str("target", target).
		Str("reason", reason)
	if cause != nil {
		event = event.Err(cause)
	}
	event.Msg("optional socket bridge skipped")
	message := reason
	if cause != nil {
		message += ": " + cause.Error()
	}
	if _, err := fmt.Fprintf(
		cmdOpts.IOStreams.ErrOut,
		"%s Optional socket bridge %s was skipped: %s\n",
		cmdOpts.IOStreams.ColorScheme().WarningIcon(),
		target,
		message,
	); err != nil {
		return fmt.Errorf("write optional socket bridge notice: %w", err)
	}
	return nil
}

func printSocketApproval(cmdOpts CommandOpts, hostPath string) error {
	if _, err := fmt.Fprintf(
		cmdOpts.IOStreams.Out,
		"%s Approved socket bridge: %s\n",
		cmdOpts.IOStreams.ColorScheme().SuccessIcon(),
		hostPath,
	); err != nil {
		return fmt.Errorf("write socket approval: %w", err)
	}
	return nil
}

func candidateBridges(candidates []socketCandidate) []socketbridge.BridgedSocket {
	bridges := make([]socketbridge.BridgedSocket, 0, len(candidates))
	for _, candidate := range candidates {
		bridges = append(bridges, candidate.bridge())
	}
	return bridges
}

func (c socketCandidate) bridge() socketbridge.BridgedSocket {
	return socketbridge.BridgedSocket{
		HostPath: c.hostPath,
		Target:   c.declaration.Target,
		Identity: c.identity,
		Group:    c.declaration.Container.Group,
		Mode:     c.declaration.Container.Mode,
	}
}

func listenerIdentityMatches(approved, observed socketbridge.ListenerIdentity) bool {
	return approved.UID == observed.UID && approved.GID == observed.GID
}

func formatListenerIdentity(identity socketbridge.ListenerIdentity) string {
	return fmt.Sprintf(
		"%s:%s (uid %d, gid %d)",
		text.SanitizeSingleLine(identity.Owner),
		text.SanitizeSingleLine(identity.Group),
		identity.UID,
		identity.GID,
	)
}

func bannedSocketPath(
	path string,
) (string, bool) {
	return bannedSocketPathFrom(path, consts.BannedSocketPaths())
}

func bannedSocketPathFrom(path string, bannedPaths []string) (string, bool) {
	for _, banned := range bannedPaths {
		if path == banned {
			return banned, true
		}
		resolved, err := cmdutil.ResolveHostPath(banned)
		if err != nil {
			// A banned path that is absent on this host cannot alias path.
			continue
		}
		if path == resolved {
			return banned, true
		}
	}
	return "", false
}

func storedDenyError(hostPath string, id int64) error {
	return fmt.Errorf(
		"socket bridge %s is denied by grant %d; run `clawker sockets revoke %d` to remove the denial",
		hostPath,
		id,
		id,
	)
}

func missingGrantError(hostPath string) error {
	return fmt.Errorf(
		"socket bridge %s is not approved; rerun with --%s or run an interactive start",
		hostPath,
		cmdutil.FlagApproveGrants,
	)
}
