package shared_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/cmd/container/shared"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/db"
	dbmocks "github.com/schmitthub/clawker/internal/db/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/internal/testenv"
)

type socketAuthorizationFixture struct {
	opts        shared.CommandOpts
	harness     shared.RuntimeHarness
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

type promptAnswerCase struct {
	name       string
	answer     string
	optional   bool
	wantBridge bool
	wantGrant  bool
	wantDeny   bool
	wantErr    bool
	wantNotice bool
}

func newSocketAuthorizationFixture(t *testing.T, tier bundle.Tier) *socketAuthorizationFixture {
	t.Helper()
	env := testenv.New(t)
	principal := filepath.Join(env.Dirs.Base, "harness")
	require.NoError(t, os.MkdirAll(principal, 0o755))
	hostPath := filepath.Join(env.Dirs.Base, "service.sock")
	listener, err := net.Listen("unix", hostPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		if closeErr := listener.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			require.NoError(t, closeErr)
		}
	})
	identity, err := socketbridge.ReadListenerIdentity(hostPath)
	require.NoError(t, err)
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
	var store dbmocks.SocketGrantStoreMock
	ios, in, out, errOut := iostreams.Test()
	fixture := &socketAuthorizationFixture{
		opts: shared.CommandOpts{ //nolint:exhaustruct,exhaustruct_v5 // The authorization helper uses only these command dependencies.
			IOStreams: ios,
			SocketGrants: func() (db.SocketGrantStore, error) {
				return &store, nil
			},
			Prompter: func() *prompter.Prompter {
				return prompter.NewPrompter(ios)
			},
		},
		harness: shared.RuntimeHarness{
			Name: "acme",
			Provenance: bundle.Provenance{
				Tier:    tier,
				Dir:     principal,
				Bundle:  bundle.BundleID{Namespace: "", Name: ""},
				Shadows: nil,
			},
			Sockets:           []config.HarnessSocket{declaration},
			Egress:            nil,
			HasContainerLabel: true,
		},
		store:       &store,
		ios:         ios,
		in:          in,
		out:         out,
		errOut:      errOut,
		principal:   principal,
		hostPath:    hostPath,
		declaration: declaration,
		identity:    identity,
	}
	return fixture
}

func decisionGrant(id int64, status string, identity socketbridge.ListenerIdentity) *db.SocketGrant {
	return &db.SocketGrant{
		ID:          id,
		HarnessPath: "",
		HarnessName: "",
		HostPath:    "",
		Status:      status,
		Identity:    identity,
		Purpose:     "",
		GrantedAt:   time.Time{},
	}
}

func (f *socketAuthorizationFixture) authorize(
	t *testing.T,
	hasHarnessLabel bool,
) ([]socketbridge.BridgedSocket, error) {
	t.Helper()
	f.harness.HasContainerLabel = hasHarnessLabel
	f.harness.Sockets = []config.HarnessSocket{f.declaration}
	return shared.AuthorizeSocketBridgesForTest(
		t.Context(),
		"clawker.project.agent",
		f.harness,
		f.opts,
		logger.Nop(),
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
			fixture.opts.ApproveGrants = true
			fixture.store.LookupSocketGrantFunc = func(ctx context.Context, _, _ string) (*db.SocketGrant, error) {
				assert.Same(t, t.Context(), ctx)
				return nil, db.ErrGrantNotFound
			}
			var prunedPrincipal string
			var prunedPaths []string
			fixture.store.PruneHarnessSocketsFunc = func(ctx context.Context, principal string, paths []string) error {
				assert.Same(t, t.Context(), ctx)
				prunedPrincipal = principal
				prunedPaths = append([]string(nil), paths...)
				return nil
			}
			var granted bool
			fixture.store.GrantSocketFunc = func(ctx context.Context, principal, harness, hostPath string, declaration config.HarnessSocket, identity socketbridge.ListenerIdentity) error {
				assert.Same(t, t.Context(), ctx)
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
	fixture.store.PruneHarnessSocketsFunc = func(context.Context, string, []string) error { return nil }
	fixture.store.LookupSocketGrantFunc = func(context.Context, string, string) (*db.SocketGrant, error) {
		return nil, db.ErrGrantNotFound
	}

	bridges, err := fixture.authorize(t, true)

	assert.Empty(t, bridges)
	require.Error(t, err)
	require.ErrorContains(t, err, "--approve-grants")
	require.ErrorContains(t, err, "interactive start")
}

func TestAuthorizeSocketBridgesStoredAllowUsesCurrentIdentity(t *testing.T) {
	fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
	fixture.store.PruneHarnessSocketsFunc = func(context.Context, string, []string) error { return nil }
	fixture.store.LookupSocketGrantFunc = func(context.Context, string, string) (*db.SocketGrant, error) {
		return decisionGrant(7, db.GrantAllow, fixture.identity), nil
	}

	bridges, err := fixture.authorize(t, true)

	require.NoError(t, err)
	require.Len(t, bridges, 1)
	assert.Equal(t, fixture.identity, bridges[0].Identity)
	assert.Empty(t, fixture.out.String())
	assert.Empty(t, fixture.errOut.String())
}

func TestAuthorizeSocketBridgesLookupFailureDoesNotPrompt(t *testing.T) {
	fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
	fixture.ios.SetStdinTTY(true)
	fixture.ios.SetStdoutTTY(true)
	fixture.in.WriteString("yes\n")
	fixture.store.PruneHarnessSocketsFunc = func(context.Context, string, []string) error { return nil }
	lookupErr := errors.New("lookup failed")
	fixture.store.LookupSocketGrantFunc = func(context.Context, string, string) (*db.SocketGrant, error) {
		return nil, lookupErr
	}

	bridges, err := fixture.authorize(t, true)

	assert.Empty(t, bridges)
	require.ErrorIs(t, err, lookupErr)
	assert.NotContains(t, fixture.errOut.String(), "Allow this socket bridge?")
}

func TestAuthorizeSocketBridgesIdentityDriftPrompts(t *testing.T) {
	fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
	fixture.ios.SetStdinTTY(true)
	fixture.ios.SetStdoutTTY(true)
	fixture.in.WriteString("yes\n")
	oldIdentity := socketbridge.ListenerIdentity{UID: 2001, GID: 2002, Owner: "old-user", Group: "old-group"}
	fixture.store.PruneHarnessSocketsFunc = func(context.Context, string, []string) error { return nil }
	fixture.store.LookupSocketGrantFunc = func(context.Context, string, string) (*db.SocketGrant, error) {
		return decisionGrant(7, db.GrantAllow, oldIdentity), nil
	}

	bridges, err := fixture.authorize(t, true)

	require.NoError(t, err)
	require.Len(t, bridges, 1)
	assert.Equal(t, fixture.identity, bridges[0].Identity)
	assert.Contains(t, fixture.errOut.String(), "Previously approved listener")
	assert.Contains(t, fixture.errOut.String(), "Current listener")
}

func TestAuthorizeSocketBridgesSanitizesHarnessPromptText(t *testing.T) {
	fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
	fixture.ios.SetStdinTTY(true)
	fixture.ios.SetStdoutTTY(true)
	fixture.in.WriteString("yes\n")
	fixture.declaration.Purpose = "\x1b[31mHost service\x1b[0m\nTraffic on this bridge is safe."
	oldIdentity := socketbridge.ListenerIdentity{
		UID:   2001,
		GID:   2002,
		Owner: "\x1b[2Jold-user\nCurrent listener: trusted",
		Group: "old-group\rPurpose: trusted",
	}
	fixture.store.PruneHarnessSocketsFunc = func(context.Context, string, []string) error { return nil }
	fixture.store.LookupSocketGrantFunc = func(context.Context, string, string) (*db.SocketGrant, error) {
		return decisionGrant(7, db.GrantAllow, oldIdentity), nil
	}

	bridges, err := fixture.authorize(t, true)

	require.NoError(t, err)
	require.Len(t, bridges, 1)
	prompt := fixture.errOut.String()
	assert.Contains(t, prompt, "Purpose: Host service\n")
	assert.Contains(t, prompt, "old-user:old-group (uid 2001, gid 2002)")
	assert.Equal(t, 1, strings.Count(prompt, "Traffic on this bridge bypasses the egress firewall."))
	assert.NotContains(t, prompt, "\x1b")
	assert.NotContains(t, prompt, "Traffic on this bridge is safe.")
	assert.NotContains(t, prompt, "Current listener: trusted")
	assert.NotContains(t, prompt, "Purpose: trusted")
	assert.NotContains(t, prompt, "\r")
}

func TestAuthorizeSocketBridgesUsesCommandPrompter(t *testing.T) {
	fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
	fixture.ios.SetStdinTTY(true)
	fixture.ios.SetStdoutTTY(true)
	fixture.in.WriteString("no\n")
	promptIO, promptIn, _, promptErrOut := iostreams.Test()
	promptIO.SetStdinTTY(true)
	promptIO.SetStdoutTTY(true)
	promptIn.WriteString("yes\n")
	prompterCalls := 0
	fixture.opts.Prompter = func() *prompter.Prompter {
		prompterCalls++
		return prompter.NewPrompter(promptIO)
	}
	fixture.store.PruneHarnessSocketsFunc = func(context.Context, string, []string) error { return nil }
	fixture.store.LookupSocketGrantFunc = func(context.Context, string, string) (*db.SocketGrant, error) {
		return nil, db.ErrGrantNotFound
	}

	bridges, err := fixture.authorize(t, true)

	require.NoError(t, err)
	require.Len(t, bridges, 1)
	assert.Equal(t, 1, prompterCalls)
	assert.NotContains(t, fixture.errOut.String(), "Allow this socket bridge?")
	assert.Contains(t, promptErrOut.String(), "Allow this socket bridge?")
}

func TestAuthorizeSocketBridgesStoredDeny(t *testing.T) {
	for _, optional := range []bool{false, true} {
		t.Run(map[bool]string{false: "required", true: "optional"}[optional], func(t *testing.T) {
			fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
			fixture.declaration.Optional = optional
			fixture.opts.ApproveGrants = true
			fixture.store.PruneHarnessSocketsFunc = func(context.Context, string, []string) error { return nil }
			fixture.store.LookupSocketGrantFunc = func(context.Context, string, string) (*db.SocketGrant, error) {
				return decisionGrant(19, db.GrantDeny, fixture.identity), nil
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
			require.ErrorContains(t, err, "19")
			require.ErrorContains(t, err, "sockets revoke 19")
		})
	}
}

func TestAuthorizeSocketBridgesPromptAnswers(t *testing.T) {
	tests := []promptAnswerCase{
		{
			name:       "always",
			answer:     "always\n",
			optional:   false,
			wantBridge: true,
			wantGrant:  true,
			wantDeny:   false,
			wantErr:    false,
			wantNotice: false,
		},
		{
			name:       "yes",
			answer:     "y\n",
			optional:   false,
			wantBridge: true,
			wantGrant:  false,
			wantDeny:   false,
			wantErr:    false,
			wantNotice: false,
		},
		{
			name:       "no",
			answer:     "no\n",
			optional:   false,
			wantBridge: false,
			wantGrant:  false,
			wantDeny:   false,
			wantErr:    true,
			wantNotice: false,
		},
		{
			name:       "never",
			answer:     "v\n",
			optional:   false,
			wantBridge: false,
			wantGrant:  false,
			wantDeny:   true,
			wantErr:    true,
			wantNotice: false,
		},
		{
			name:       "optional no",
			answer:     "n\n",
			optional:   true,
			wantBridge: false,
			wantGrant:  false,
			wantDeny:   false,
			wantErr:    false,
			wantNotice: true,
		},
		{
			name:       "optional never",
			answer:     "never\n",
			optional:   true,
			wantBridge: false,
			wantGrant:  false,
			wantDeny:   true,
			wantErr:    false,
			wantNotice: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
			fixture.declaration.Optional = tt.optional
			fixture.ios.SetStdinTTY(true)
			fixture.ios.SetStdoutTTY(true)
			fixture.in.WriteString(tt.answer)
			fixture.store.PruneHarnessSocketsFunc = func(context.Context, string, []string) error { return nil }
			fixture.store.LookupSocketGrantFunc = promptAnswerLookup(tt, fixture.identity)
			granted := false
			fixture.store.GrantSocketFunc = func(context.Context, string, string, string, config.HarnessSocket, socketbridge.ListenerIdentity) error {
				granted = true
				return nil
			}
			denied := false
			fixture.store.DenySocketFunc = func(context.Context, string, string, string, config.HarnessSocket, socketbridge.ListenerIdentity) error {
				denied = true
				return nil
			}

			bridges, err := fixture.authorize(t, true)

			assertPromptAnswerResult(t, tt, fixture, bridges, err, granted, denied)
		})
	}
}

func promptAnswerLookup(
	testCase promptAnswerCase,
	identity socketbridge.ListenerIdentity,
) func(context.Context, string, string) (*db.SocketGrant, error) {
	lookups := 0
	return func(context.Context, string, string) (*db.SocketGrant, error) {
		lookups++
		if testCase.wantDeny && lookups > 1 {
			return decisionGrant(33, db.GrantDeny, identity), nil
		}
		return nil, db.ErrGrantNotFound
	}
}

func assertPromptAnswerResult(
	t *testing.T,
	testCase promptAnswerCase,
	fixture *socketAuthorizationFixture,
	bridges []socketbridge.BridgedSocket,
	resultErr error,
	granted,
	denied bool,
) {
	t.Helper()
	assert.Equal(t, testCase.wantBridge, len(bridges) == 1)
	assert.Equal(t, testCase.wantGrant, granted)
	assert.Equal(t, testCase.wantDeny, denied)
	if testCase.wantErr {
		require.Error(t, resultErr)
	} else {
		require.NoError(t, resultErr)
	}
	if testCase.wantDeny && testCase.wantErr {
		require.ErrorContains(t, resultErr, "33")
		require.ErrorContains(t, resultErr, "sockets revoke 33")
	}
	if testCase.name == "no" {
		require.ErrorContains(t, resultErr, "--approve-grants")
		require.ErrorContains(t, resultErr, "interactive start")
	}
	if testCase.wantNotice {
		assert.Contains(t, fixture.errOut.String(), "was skipped")
	}
	assert.Contains(t, fixture.errOut.String(), fixture.declaration.Target)
	assert.Contains(t, fixture.errOut.String(), "Purpose: "+fixture.declaration.Purpose)
	assert.Contains(t, fixture.errOut.String(), shared.FormatListenerIdentityForTest(fixture.identity))
}

func TestAuthorizeSocketBridgesInvalidPromptAnswersAskAgain(t *testing.T) {
	fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
	fixture.ios.SetStdinTTY(true)
	fixture.ios.SetStdoutTTY(true)
	fixture.in.WriteString("\nmaybe\nyes\n")
	fixture.store.PruneHarnessSocketsFunc = func(context.Context, string, []string) error { return nil }
	fixture.store.LookupSocketGrantFunc = func(context.Context, string, string) (*db.SocketGrant, error) {
		return nil, db.ErrGrantNotFound
	}

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
	require.ErrorContains(t, err, "clawker.project.agent")
	require.ErrorContains(t, err, consts.LabelHarness)
	assert.False(t, storeOpened)
}

func TestBannedSocketPathResolvesAliases(t *testing.T) {
	env := testenv.New(t)
	realPath := filepath.Join(env.Dirs.Base, "daemon.sock")
	aliasPath := filepath.Join(env.Dirs.Base, "daemon-alias.sock")
	require.NoError(t, os.WriteFile(realPath, nil, 0o600))
	require.NoError(t, os.Symlink(realPath, aliasPath))

	banned, found := shared.BannedSocketPathFromForTest(realPath, []string{aliasPath})

	assert.True(t, found)
	assert.Equal(t, aliasPath, banned)
}

func TestAuthorizeSocketBridgesOptionalUnapprovedNoninteractivePrintsNotice(t *testing.T) {
	fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
	fixture.declaration.Optional = true
	fixture.store.PruneHarnessSocketsFunc = func(context.Context, string, []string) error { return nil }
	fixture.store.LookupSocketGrantFunc = func(context.Context, string, string) (*db.SocketGrant, error) {
		return nil, db.ErrGrantNotFound
	}

	bridges, err := fixture.authorize(t, true)

	require.NoError(t, err)
	assert.Empty(t, bridges)
	assert.Contains(t, fixture.errOut.String(), "was skipped")
	assert.Contains(t, fixture.errOut.String(), "interactive start")
}

func TestAuthorizeSocketBridgesOptionalProbeFailurePrintsNotice(t *testing.T) {
	fixture := newSocketAuthorizationFixture(t, bundle.TierLooseProject)
	fixture.declaration.Optional = true
	regularPath := filepath.Join(fixture.principal, "regular-file")
	require.NoError(t, os.WriteFile(regularPath, nil, 0o600))
	fixture.declaration.Source = regularPath
	fixture.store.PruneHarnessSocketsFunc = func(_ context.Context, principal string, paths []string) error {
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
	fixture.declaration.Source = filepath.Join(fixture.principal, "missing.sock")
	fixture.store.PruneHarnessSocketsFunc = func(_ context.Context, principal string, paths []string) error {
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
