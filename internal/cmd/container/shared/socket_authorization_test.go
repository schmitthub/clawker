package shared

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/db"
	dbmocks "github.com/schmitthub/clawker/internal/db/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/socketbridge"
)

type socketAuthorizationFixture struct {
	cfg         config.Config
	opts        CommandOpts
	deps        socketAuthorizationDeps
	store       *dbmocks.SocketGrantStoreMock
	ios         *iostreams.IOStreams
	in          *bytes.Buffer
	out         *bytes.Buffer
	errOut      *bytes.Buffer
	principal   string
	hostPath    string
	declaration config.HarnessSocket
	identity    socketbridge.ListenerIdentity
}

func newSocketAuthorizationFixture(t *testing.T, tier bundle.Tier) *socketAuthorizationFixture {
	t.Helper()
	principal := filepath.Join(t.TempDir(), "harness")
	require.NoError(t, os.MkdirAll(principal, 0o755))
	hostPath := filepath.Join(t.TempDir(), "service.sock")
	require.NoError(t, os.WriteFile(hostPath, nil, 0o600))
	identity := socketbridge.ListenerIdentity{UID: 1001, GID: 1002, Owner: "host-user", Group: "host-group"}
	declaration := config.HarnessSocket{
		Source:   hostPath,
		Target:   "/home/clawker/service.sock",
		Purpose:  "Connect to the host service.",
		Optional: false,
		Container: config.HarnessSocketContainer{
			Group: "service",
			Mode:  "0660",
		},
	}
	store := &dbmocks.SocketGrantStoreMock{}
	ios, in, out, errOut := iostreams.Test()
	fixture := &socketAuthorizationFixture{
		cfg: configmocks.NewBlankConfig(),
		opts: CommandOpts{ //nolint:exhaustruct // the authorization helper uses only these command dependencies
			IOStreams: ios,
			SocketGrants: func() (db.SocketGrantStore, error) {
				return store, nil
			},
		},
		deps: socketAuthorizationDeps{
			resolveHarness: func(config.Config, string) (resolvedSocketHarness, error) {
				return resolvedSocketHarness{
					Provenance: bundle.Provenance{Tier: tier, Dir: principal, Bundle: bundle.BundleID{}, Shadows: nil},
					Sockets:    []config.HarnessSocket{declaration},
				}, nil
			},
			resolveHostPath: cmdutil.ResolveHostPath,
			readListenerIdentity: func(string) (socketbridge.ListenerIdentity, error) {
				return identity, nil
			},
		},
		store: store, ios: ios, in: in, out: out, errOut: errOut,
		principal: principal, hostPath: hostPath, declaration: declaration, identity: identity,
	}
	return fixture
}

func (f *socketAuthorizationFixture) authorize(
	t *testing.T,
	hasHarnessLabel bool,
) ([]socketbridge.BridgedSocket, error) {
	t.Helper()
	return authorizeSocketBridges(
		"clawker.project.agent",
		"acme",
		hasHarnessLabel,
		f.cfg,
		f.opts,
		logger.Nop(),
		f.deps,
	)
}

func TestAuthorizeSocketBridgesBuiltInSkipsStore(t *testing.T) {
	fixture := newSocketAuthorizationFixture(t, bundle.TierFloor)
	storeOpened := false
	fixture.opts.SocketGrants = func() (db.SocketGrantStore, error) {
		storeOpened = true
		return fixture.store, nil
	}

	bridges, err := fixture.authorize(t, true)

	require.NoError(t, err)
	assert.False(t, storeOpened)
	assert.Equal(t, []socketbridge.BridgedSocket{{
		HostPath: fixture.hostPath,
		Target:   fixture.declaration.Target,
		Identity: fixture.identity,
		Group:    fixture.declaration.Container.Group,
		Mode:     fixture.declaration.Container.Mode,
	}}, bridges)
}

func TestAuthorizeSocketBridgesApproveFlagPersistsRequiredAndOptional(t *testing.T) {
	for _, optional := range []bool{false, true} {
		t.Run(map[bool]string{false: "required", true: "optional"}[optional], func(t *testing.T) {
			fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
			fixture.declaration.Optional = optional
			fixture.deps.resolveHarness = func(config.Config, string) (resolvedSocketHarness, error) {
				return resolvedSocketHarness{
					Provenance: bundle.Provenance{
						Tier:    bundle.TierLooseProject,
						Dir:     fixture.principal,
						Bundle:  bundle.BundleID{},
						Shadows: nil,
					},
					Sockets: []config.HarnessSocket{fixture.declaration},
				}, nil
			}
			fixture.opts.ApproveGrants = true
			fixture.store.LookupSocketGrantFunc = func(string, string) (*db.SocketGrant, error) { return nil, nil }
			var prunedPrincipal string
			var prunedPaths []string
			fixture.store.PruneHarnessSocketsFunc = func(principal string, paths []string) error {
				prunedPrincipal = principal
				prunedPaths = append([]string(nil), paths...)
				return nil
			}
			var granted bool
			fixture.store.GrantSocketFunc = func(principal, harness, hostPath string, declaration config.HarnessSocket, identity socketbridge.ListenerIdentity) error {
				granted = true
				assert.Equal(t, fixture.principal, principal)
				assert.Equal(t, "acme", harness)
				assert.Equal(t, fixture.hostPath, hostPath)
				assert.Equal(t, fixture.declaration, declaration)
				assert.Equal(t, fixture.identity, identity)
				return nil
			}

			bridges, err := fixture.authorize(t, true)

			require.NoError(t, err)
			assert.True(t, granted)
			assert.Equal(t, fixture.principal, prunedPrincipal)
			assert.Equal(t, []string{fixture.hostPath}, prunedPaths)
			require.Len(t, bridges, 1)
			assert.Contains(t, fixture.out.String(), fixture.hostPath)
		})
	}
}

func TestAuthorizeSocketBridgesNoninteractiveFailsClosedWithRemedies(t *testing.T) {
	fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
	fixture.store.PruneHarnessSocketsFunc = func(string, []string) error { return nil }
	fixture.store.LookupSocketGrantFunc = func(string, string) (*db.SocketGrant, error) { return nil, nil }

	bridges, err := fixture.authorize(t, true)

	assert.Empty(t, bridges)
	require.Error(t, err)
	assert.ErrorContains(t, err, "--approve-grants")
	assert.ErrorContains(t, err, "interactive start")
}

func TestAuthorizeSocketBridgesStoredAllowUsesCurrentIdentity(t *testing.T) {
	fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
	fixture.store.PruneHarnessSocketsFunc = func(string, []string) error { return nil }
	fixture.store.LookupSocketGrantFunc = func(string, string) (*db.SocketGrant, error) {
		return &db.SocketGrant{
			ID:       7,
			Status:   db.GrantAllow,
			Identity: fixture.identity,
		}, nil //nolint:exhaustruct // only decision fields are read
	}

	bridges, err := fixture.authorize(t, true)

	require.NoError(t, err)
	require.Len(t, bridges, 1)
	assert.Equal(t, fixture.identity, bridges[0].Identity)
	assert.Empty(t, fixture.out.String())
	assert.Empty(t, fixture.errOut.String())
}

func TestAuthorizeSocketBridgesIdentityDriftPrompts(t *testing.T) {
	fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
	fixture.ios.SetStdinTTY(true)
	fixture.ios.SetStdoutTTY(true)
	fixture.in.WriteString("yes\n")
	oldIdentity := socketbridge.ListenerIdentity{UID: 2001, GID: 2002, Owner: "old-user", Group: "old-group"}
	fixture.store.PruneHarnessSocketsFunc = func(string, []string) error { return nil }
	fixture.store.LookupSocketGrantFunc = func(string, string) (*db.SocketGrant, error) {
		return &db.SocketGrant{
			ID:       7,
			Status:   db.GrantAllow,
			Identity: oldIdentity,
		}, nil //nolint:exhaustruct // only decision fields are read
	}

	bridges, err := fixture.authorize(t, true)

	require.NoError(t, err)
	require.Len(t, bridges, 1)
	assert.Equal(t, fixture.identity, bridges[0].Identity)
	assert.Contains(t, fixture.errOut.String(), "Previously approved listener")
	assert.Contains(t, fixture.errOut.String(), "Current listener")
}

func TestAuthorizeSocketBridgesStoredDeny(t *testing.T) {
	for _, optional := range []bool{false, true} {
		t.Run(map[bool]string{false: "required", true: "optional"}[optional], func(t *testing.T) {
			fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
			fixture.declaration.Optional = optional
			fixture.deps.resolveHarness = func(config.Config, string) (resolvedSocketHarness, error) {
				return resolvedSocketHarness{
					Provenance: bundle.Provenance{
						Tier:    bundle.TierLooseProject,
						Dir:     fixture.principal,
						Bundle:  bundle.BundleID{},
						Shadows: nil,
					},
					Sockets: []config.HarnessSocket{fixture.declaration},
				}, nil
			}
			fixture.opts.ApproveGrants = true
			fixture.store.PruneHarnessSocketsFunc = func(string, []string) error { return nil }
			fixture.store.LookupSocketGrantFunc = func(string, string) (*db.SocketGrant, error) {
				return &db.SocketGrant{
					ID:       19,
					Status:   db.GrantDeny,
					Identity: fixture.identity,
				}, nil //nolint:exhaustruct // only decision fields are read
			}

			bridges, err := fixture.authorize(t, true)

			assert.Empty(t, bridges)
			if optional {
				require.NoError(t, err)
				assert.Empty(t, fixture.out.String())
				assert.Empty(t, fixture.errOut.String())
				return
			}
			require.Error(t, err)
			assert.ErrorContains(t, err, "19")
			assert.ErrorContains(t, err, "sockets revoke 19")
		})
	}
}

func TestAuthorizeSocketBridgesPromptAnswers(t *testing.T) {
	tests := []struct {
		name       string
		answer     string
		optional   bool
		wantBridge bool
		wantGrant  bool
		wantDeny   bool
		wantErr    bool
		wantNotice bool
	}{
		{name: "always", answer: "always\n", wantBridge: true, wantGrant: true},
		{name: "yes", answer: "y\n", wantBridge: true},
		{name: "no", answer: "no\n", wantErr: true},
		{name: "never", answer: "v\n", wantDeny: true, wantErr: true},
		{name: "optional no", answer: "n\n", optional: true, wantNotice: true},
		{name: "optional never", answer: "never\n", optional: true, wantDeny: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
			fixture.declaration.Optional = tt.optional
			fixture.deps.resolveHarness = func(config.Config, string) (resolvedSocketHarness, error) {
				return resolvedSocketHarness{
					Provenance: bundle.Provenance{
						Tier:    bundle.TierLooseProject,
						Dir:     fixture.principal,
						Bundle:  bundle.BundleID{},
						Shadows: nil,
					},
					Sockets: []config.HarnessSocket{fixture.declaration},
				}, nil
			}
			fixture.ios.SetStdinTTY(true)
			fixture.ios.SetStdoutTTY(true)
			fixture.in.WriteString(tt.answer)
			fixture.store.PruneHarnessSocketsFunc = func(string, []string) error { return nil }
			lookups := 0
			fixture.store.LookupSocketGrantFunc = func(string, string) (*db.SocketGrant, error) {
				lookups++
				if tt.wantDeny && lookups > 1 {
					return &db.SocketGrant{
						ID:       33,
						Status:   db.GrantDeny,
						Identity: fixture.identity,
					}, nil //nolint:exhaustruct // only decision fields are read
				}
				return nil, nil
			}
			granted := false
			fixture.store.GrantSocketFunc = func(string, string, string, config.HarnessSocket, socketbridge.ListenerIdentity) error {
				granted = true
				return nil
			}
			denied := false
			fixture.store.DenySocketFunc = func(string, string, string, config.HarnessSocket, socketbridge.ListenerIdentity) error {
				denied = true
				return nil
			}

			bridges, err := fixture.authorize(t, true)

			assert.Equal(t, tt.wantBridge, len(bridges) == 1)
			assert.Equal(t, tt.wantGrant, granted)
			assert.Equal(t, tt.wantDeny, denied)
			assert.Equal(t, tt.wantErr, err != nil)
			if tt.wantDeny {
				if tt.wantErr {
					assert.ErrorContains(t, err, "33")
					assert.ErrorContains(t, err, "sockets revoke 33")
				}
			}
			if tt.name == "no" {
				assert.ErrorContains(t, err, "--approve-grants")
				assert.ErrorContains(t, err, "interactive start")
			}
			if tt.wantNotice {
				assert.Contains(t, fixture.errOut.String(), "was skipped")
			}
			assert.Contains(t, fixture.errOut.String(), fixture.declaration.Target)
			assert.Contains(t, fixture.errOut.String(), "Purpose: "+fixture.declaration.Purpose)
			assert.Contains(t, fixture.errOut.String(), "host-user:host-group (uid 1001, gid 1002)")
		})
	}
}

func TestAuthorizeSocketBridgesInvalidPromptAnswersAskAgain(t *testing.T) {
	fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
	fixture.ios.SetStdinTTY(true)
	fixture.ios.SetStdoutTTY(true)
	fixture.in.WriteString("\nmaybe\nyes\n")
	fixture.store.PruneHarnessSocketsFunc = func(string, []string) error { return nil }
	fixture.store.LookupSocketGrantFunc = func(string, string) (*db.SocketGrant, error) { return nil, nil }

	bridges, err := fixture.authorize(t, true)

	require.NoError(t, err)
	require.Len(t, bridges, 1)
	assert.Equal(t, 3, strings.Count(fixture.errOut.String(), "Allow this socket bridge?"))
}

func TestAuthorizeSocketBridgesRequiresExplicitHarnessLabel(t *testing.T) {
	fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
	storeOpened := false
	fixture.opts.SocketGrants = func() (db.SocketGrantStore, error) {
		storeOpened = true
		return fixture.store, nil
	}

	bridges, err := fixture.authorize(t, false)

	assert.Empty(t, bridges)
	require.Error(t, err)
	assert.ErrorContains(t, err, "clawker.project.agent")
	assert.ErrorContains(t, err, consts.LabelHarness)
	assert.False(t, storeOpened)
}

func TestAuthorizeSocketBridgesBannedPathFailsBeforeProbe(t *testing.T) {
	for _, tier := range []bundle.Tier{
		bundle.TierFloor,
		bundle.TierLooseProject,
		bundle.TierLooseUser,
		bundle.TierInstalled,
		bundle.TierInPlace,
	} {
		t.Run(tier.Label(), func(t *testing.T) {
			fixture := newSocketAuthorizationFixture(t, tier)
			fixture.declaration.Optional = true
			fixture.deps.resolveHarness = func(config.Config, string) (resolvedSocketHarness, error) {
				return resolvedSocketHarness{
					Provenance: bundle.Provenance{
						Tier:    tier,
						Dir:     fixture.principal,
						Bundle:  bundle.BundleID{},
						Shadows: nil,
					},
					Sockets: []config.HarnessSocket{fixture.declaration},
				}, nil
			}
			fixture.deps.resolveHostPath = func(string) (string, error) { return consts.DockerSocketPath, nil }
			probed := false
			fixture.deps.readListenerIdentity = func(string) (socketbridge.ListenerIdentity, error) {
				probed = true
				return fixture.identity, nil
			}

			bridges, err := fixture.authorize(t, true)

			assert.Empty(t, bridges)
			require.Error(t, err)
			assert.ErrorContains(t, err, consts.DockerSocketPath)
			assert.ErrorContains(t, err, "security.docker_socket")
			assert.False(t, probed)
		})
	}
}

func TestAuthorizeSocketBridgesResolvedBannedPathFailsBeforeProbe(t *testing.T) {
	const (
		aliasPath    = "/tmp/docker-alias.sock"
		resolvedPath = "/host/docker-desktop/docker.sock"
	)
	fixture := newSocketAuthorizationFixture(t, bundle.TierFloor)
	fixture.declaration.Source = aliasPath
	fixture.deps.resolveHarness = func(config.Config, string) (resolvedSocketHarness, error) {
		return resolvedSocketHarness{
			Provenance: bundle.Provenance{
				Tier:    bundle.TierFloor,
				Dir:     fixture.principal,
				Bundle:  bundle.BundleID{},
				Shadows: nil,
			},
			Sockets: []config.HarnessSocket{fixture.declaration},
		}, nil
	}
	fixture.deps.resolveHostPath = func(expression string) (string, error) {
		switch expression {
		case aliasPath, consts.DockerSocketPath:
			return resolvedPath, nil
		default:
			return cmdutil.ResolveHostPath(expression)
		}
	}
	probed := false
	fixture.deps.readListenerIdentity = func(string) (socketbridge.ListenerIdentity, error) {
		probed = true
		return fixture.identity, nil
	}

	bridges, err := fixture.authorize(t, true)

	assert.Empty(t, bridges)
	require.Error(t, err)
	assert.ErrorContains(t, err, consts.DockerSocketPath)
	assert.ErrorContains(t, err, "security.docker_socket")
	assert.False(t, probed)
}

func TestAuthorizeSocketBridgesOptionalUnapprovedNoninteractivePrintsNotice(t *testing.T) {
	fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
	fixture.declaration.Optional = true
	fixture.deps.resolveHarness = func(config.Config, string) (resolvedSocketHarness, error) {
		return resolvedSocketHarness{
			Provenance: bundle.Provenance{
				Tier:    bundle.TierLooseProject,
				Dir:     fixture.principal,
				Bundle:  bundle.BundleID{},
				Shadows: nil,
			},
			Sockets: []config.HarnessSocket{fixture.declaration},
		}, nil
	}
	fixture.store.PruneHarnessSocketsFunc = func(string, []string) error { return nil }
	fixture.store.LookupSocketGrantFunc = func(string, string) (*db.SocketGrant, error) { return nil, nil }

	bridges, err := fixture.authorize(t, true)

	require.NoError(t, err)
	assert.Empty(t, bridges)
	assert.Contains(t, fixture.errOut.String(), "was skipped")
	assert.Contains(t, fixture.errOut.String(), "interactive start")
}

func TestAuthorizeSocketBridgesOptionalProbeFailurePrintsNotice(t *testing.T) {
	fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
	fixture.declaration.Optional = true
	fixture.deps.resolveHarness = func(config.Config, string) (resolvedSocketHarness, error) {
		return resolvedSocketHarness{
			Provenance: bundle.Provenance{
				Tier:    bundle.TierLooseProject,
				Dir:     fixture.principal,
				Bundle:  bundle.BundleID{},
				Shadows: nil,
			},
			Sockets: []config.HarnessSocket{fixture.declaration},
		}, nil
	}
	fixture.deps.readListenerIdentity = func(string) (socketbridge.ListenerIdentity, error) {
		return socketbridge.ListenerIdentity{}, errors.New("listener unavailable")
	}
	fixture.store.PruneHarnessSocketsFunc = func(principal string, paths []string) error {
		assert.Equal(t, fixture.principal, principal)
		assert.Empty(t, paths)
		return nil
	}

	bridges, err := fixture.authorize(t, true)

	require.NoError(t, err)
	assert.Empty(t, bridges)
	assert.Contains(t, fixture.errOut.String(), "listener probe failed")
}

func TestAuthorizeSocketBridgesOptionalResolutionFailurePrintsNotice(t *testing.T) {
	fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
	fixture.declaration.Optional = true
	fixture.deps.resolveHarness = func(config.Config, string) (resolvedSocketHarness, error) {
		return resolvedSocketHarness{
			Provenance: bundle.Provenance{
				Tier:    bundle.TierLooseProject,
				Dir:     fixture.principal,
				Bundle:  bundle.BundleID{},
				Shadows: nil,
			},
			Sockets: []config.HarnessSocket{fixture.declaration},
		}, nil
	}
	fixture.deps.resolveHostPath = func(value string) (string, error) {
		if value == fixture.declaration.Source {
			return "", errors.New("socket is missing")
		}
		return cmdutil.ResolveHostPath(value)
	}
	fixture.store.PruneHarnessSocketsFunc = func(principal string, paths []string) error {
		assert.Equal(t, fixture.principal, principal)
		assert.Empty(t, paths)
		return nil
	}

	bridges, err := fixture.authorize(t, true)

	require.NoError(t, err)
	assert.Empty(t, bridges)
	assert.Contains(t, fixture.errOut.String(), "Optional")
	assert.Contains(t, fixture.errOut.String(), fixture.declaration.Target)
}
