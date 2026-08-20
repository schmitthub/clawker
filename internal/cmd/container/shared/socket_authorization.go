package shared

import (
	"errors"
	"fmt"
	"strings"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/db"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/socketbridge"
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

type resolvedSocketHarness struct {
	Provenance bundle.Provenance
	Sockets    []config.HarnessSocket
}

type socketAuthorizationDeps struct {
	resolveHarness       func(config.Config, string) (resolvedSocketHarness, error)
	resolveHostPath      func(string) (string, error)
	readListenerIdentity func(string) (socketbridge.ListenerIdentity, error)
}

type socketCandidate struct {
	declaration config.HarnessSocket
	hostPath    string
	identity    socketbridge.ListenerIdentity
}

func defaultSocketAuthorizationDeps() socketAuthorizationDeps {
	return socketAuthorizationDeps{
		resolveHarness: func(cfg config.Config, name string) (resolvedSocketHarness, error) {
			component, err := bundle.NewResolver(cfg).Resolve(bundle.ComponentHarness, name)
			if err != nil {
				return resolvedSocketHarness{}, fmt.Errorf("resolve harness %q: %w", name, err)
			}
			harness, err := bundler.LoadBundle(name, component.FS)
			if err != nil {
				return resolvedSocketHarness{}, fmt.Errorf("load harness %q: %w", name, err)
			}
			return resolvedSocketHarness{
				Provenance: component.Provenance,
				Sockets:    harness.Manifest.Sockets,
			}, nil
		},
		resolveHostPath:      cmdutil.ResolveHostPath,
		readListenerIdentity: socketbridge.ReadListenerIdentity,
	}
}

func authorizeSocketBridges(
	container string,
	harnessName string,
	hasHarnessLabel bool,
	cfg config.Config,
	cmdOpts CommandOpts,
	log *logger.Logger,
	deps socketAuthorizationDeps,
) ([]socketbridge.BridgedSocket, error) {
	if log == nil {
		log = logger.Nop()
	}
	resolved, err := deps.resolveHarness(cfg, harnessName)
	if err != nil {
		return nil, fmt.Errorf("authorize socket bridges: %w", err)
	}
	if len(resolved.Sockets) == 0 {
		return nil, nil
	}
	if !hasHarnessLabel {
		return nil, fmt.Errorf(
			"container %s cannot request host sockets without the %s label; rebuild the container",
			container,
			consts.LabelHarness,
		)
	}

	candidates, err := resolveSocketCandidates(harnessName, resolved.Sockets, cmdOpts, log, deps)
	if err != nil {
		return nil, err
	}
	if resolved.Provenance.Tier == bundle.TierFloor {
		return candidateBridges(candidates), nil
	}

	principal, err := deps.resolveHostPath(resolved.Provenance.Dir)
	if err != nil {
		return nil, fmt.Errorf("authorize socket bridges for harness %q: resolve principal: %w", harnessName, err)
	}
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
	hostPaths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		hostPaths = append(hostPaths, candidate.hostPath)
	}
	if err := store.PruneHarnessSockets(principal, hostPaths); err != nil {
		return nil, fmt.Errorf("authorize socket bridges: prune harness grants: %w", err)
	}

	bridges := make([]socketbridge.BridgedSocket, 0, len(candidates))
	for _, candidate := range candidates {
		bridge, active, authorizeErr := authorizeSocketCandidate(
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
	deps socketAuthorizationDeps,
) ([]socketCandidate, error) {
	candidates := make([]socketCandidate, 0, len(declarations))
	for _, declaration := range declarations {
		hostPath, err := deps.resolveHostPath(declaration.Source)
		if err != nil {
			if declaration.Optional {
				if noticeErr := reportOptionalSocketSkip(
					cmdOpts,
					log,
					harnessName,
					declaration.Target,
					"host path did not resolve",
					err,
				); noticeErr != nil {
					return nil, noticeErr
				}
				continue
			}
			return nil, fmt.Errorf(
				"required socket bridge %s for harness %q: resolve host path: %w",
				declaration.Target,
				harnessName,
				err,
			)
		}
		bannedPath, banned := bannedSocketPath(hostPath, deps.resolveHostPath)
		if banned {
			if bannedPath == consts.DockerSocketPath {
				return nil, fmt.Errorf(
					"host socket %s is banned; use %s for Docker daemon access",
					bannedPath,
					dockerSocketSetting,
				)
			}
			return nil, fmt.Errorf("host socket %s is banned", bannedPath)
		}
		identity, err := deps.readListenerIdentity(hostPath)
		if err != nil {
			if declaration.Optional {
				if noticeErr := reportOptionalSocketSkip(
					cmdOpts,
					log,
					harnessName,
					declaration.Target,
					"listener probe failed",
					err,
				); noticeErr != nil {
					return nil, noticeErr
				}
				continue
			}
			return nil, fmt.Errorf(
				"required socket bridge %s for harness %q: probe host listener %s: %w",
				declaration.Target,
				harnessName,
				hostPath,
				err,
			)
		}
		candidates = append(candidates, socketCandidate{
			declaration: declaration,
			hostPath:    hostPath,
			identity:    identity,
		})
	}
	return candidates, nil
}

func authorizeSocketCandidate(
	harnessName string,
	principal string,
	candidate socketCandidate,
	cmdOpts CommandOpts,
	store db.SocketGrantStore,
	log *logger.Logger,
) (socketbridge.BridgedSocket, bool, error) {
	grant, err := store.LookupSocketGrant(principal, candidate.hostPath)
	if err != nil {
		return socketbridge.BridgedSocket{}, false, fmt.Errorf(
			"lookup socket grant for %s: %w",
			candidate.hostPath,
			err,
		)
	}
	if grant != nil {
		switch grant.Status {
		case db.GrantAllow:
			if listenerIdentityMatches(grant.Identity, candidate.identity) {
				return candidate.bridge(), true, nil
			}
			return authorizePromptedSocket(harnessName, principal, candidate, grant, cmdOpts, store, log)
		case db.GrantDeny:
			if candidate.declaration.Optional {
				log.Info().
					Str("event", eventStoredSocketDenied).
					Str("harness", harnessName).
					Str("host_path", candidate.hostPath).
					Str("target", candidate.declaration.Target).
					Int64("grant_id", grant.ID).
					Msg("stored socket denial applied")
				return socketbridge.BridgedSocket{}, false, nil
			}
			return socketbridge.BridgedSocket{}, false, storedDenyError(candidate.hostPath, grant.ID)
		default:
			return socketbridge.BridgedSocket{}, false, fmt.Errorf(
				"socket grant %d has unknown status %q",
				grant.ID,
				grant.Status,
			)
		}
	}

	if cmdOpts.ApproveGrants {
		if err := store.GrantSocket(
			principal,
			harnessName,
			candidate.hostPath,
			candidate.declaration,
			candidate.identity,
		); err != nil {
			return socketbridge.BridgedSocket{}, false, fmt.Errorf(
				"store socket grant for %s: %w",
				candidate.hostPath,
				err,
			)
		}
		if err := printSocketApproval(cmdOpts, candidate.hostPath); err != nil {
			return socketbridge.BridgedSocket{}, false, err
		}
		return candidate.bridge(), true, nil
	}
	return authorizePromptedSocket(harnessName, principal, candidate, nil, cmdOpts, store, log)
}

func authorizePromptedSocket(
	harnessName string,
	principal string,
	candidate socketCandidate,
	previous *db.SocketGrant,
	cmdOpts CommandOpts,
	store db.SocketGrantStore,
	log *logger.Logger,
) (socketbridge.BridgedSocket, bool, error) {
	if !cmdOpts.IOStreams.CanPrompt() {
		if candidate.declaration.Optional {
			if err := reportOptionalSocketSkip(
				cmdOpts,
				log,
				harnessName,
				candidate.declaration.Target,
				"approval requires an interactive start",
				nil,
			); err != nil {
				return socketbridge.BridgedSocket{}, false, err
			}
			return socketbridge.BridgedSocket{}, false, nil
		}
		if previous != nil {
			return socketbridge.BridgedSocket{}, false, fmt.Errorf(
				"listener identity for socket bridge %s changed; run an interactive start to approve the current listener",
				candidate.hostPath,
			)
		}
		return socketbridge.BridgedSocket{}, false, missingGrantError(candidate.hostPath)
	}

	answer, err := promptForSocketGrant(cmdOpts, harnessName, principal, candidate, previous)
	if err != nil {
		return socketbridge.BridgedSocket{}, false, err
	}
	switch answer {
	case socketAnswerAlways:
		if err := store.GrantSocket(
			principal,
			harnessName,
			candidate.hostPath,
			candidate.declaration,
			candidate.identity,
		); err != nil {
			return socketbridge.BridgedSocket{}, false, fmt.Errorf(
				"store socket grant for %s: %w",
				candidate.hostPath,
				err,
			)
		}
		if err := printSocketApproval(cmdOpts, candidate.hostPath); err != nil {
			return socketbridge.BridgedSocket{}, false, err
		}
		return candidate.bridge(), true, nil
	case socketAnswerYes:
		return candidate.bridge(), true, nil
	case socketAnswerNo:
		if candidate.declaration.Optional {
			if err := reportOptionalSocketSkip(
				cmdOpts,
				log,
				harnessName,
				candidate.declaration.Target,
				"approval was declined",
				nil,
			); err != nil {
				return socketbridge.BridgedSocket{}, false, err
			}
			return socketbridge.BridgedSocket{}, false, nil
		}
		return socketbridge.BridgedSocket{}, false, missingGrantError(candidate.hostPath)
	case socketAnswerNever:
		if err := store.DenySocket(
			principal,
			harnessName,
			candidate.hostPath,
			candidate.declaration,
			candidate.identity,
		); err != nil {
			return socketbridge.BridgedSocket{}, false, fmt.Errorf(
				"store socket denial for %s: %w",
				candidate.hostPath,
				err,
			)
		}
		if candidate.declaration.Optional {
			return socketbridge.BridgedSocket{}, false, nil
		}
		if previous != nil {
			return socketbridge.BridgedSocket{}, false, storedDenyError(candidate.hostPath, previous.ID)
		}
		stored, err := store.LookupSocketGrant(principal, candidate.hostPath)
		if err != nil {
			return socketbridge.BridgedSocket{}, false, fmt.Errorf(
				"read stored socket denial for %s: %w",
				candidate.hostPath,
				err,
			)
		}
		if stored == nil {
			return socketbridge.BridgedSocket{}, false, fmt.Errorf(
				"stored socket denial for %s was not found",
				candidate.hostPath,
			)
		}
		return socketbridge.BridgedSocket{}, false, storedDenyError(candidate.hostPath, stored.ID)
	default:
		return socketbridge.BridgedSocket{}, false, fmt.Errorf("unknown socket authorization answer %q", answer)
	}
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
		candidate.declaration.Purpose,
		optionalNotice,
	); err != nil {
		return "", fmt.Errorf("write socket authorization warning: %w", err)
	}
	prompt := prompter.NewPrompter(ios)
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
	return fmt.Sprintf("%s:%s (uid %d, gid %d)", identity.Owner, identity.Group, identity.UID, identity.GID)
}

func bannedSocketPath(
	path string,
	resolveHostPath func(string) (string, error),
) (string, bool) {
	for _, banned := range consts.BannedSocketPaths {
		if path == banned {
			return banned, true
		}
		resolved, err := resolveHostPath(banned)
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
