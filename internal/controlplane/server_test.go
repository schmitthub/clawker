package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/agentregistry"
	"github.com/schmitthub/clawker/internal/controlplane/agentslots"
	slotmocks "github.com/schmitthub/clawker/internal/controlplane/agentslots/mocks"
)

func TestAdminServer_ListAgents_NilRegistry(t *testing.T) {
	// nil registry must NOT panic — allows tests / partial wiring to
	// land cleanly. Empty result is the safe answer.
	srv := &adminServer{}
	resp, err := srv.ListAgents(context.Background(), &adminv1.ListAgentsRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp.Agents)
}

func TestAdminServer_ListAgents_Snapshot(t *testing.T) {
	reg := agentregistry.NewRegistry(nil)
	now := time.Unix(1000, 0)

	thumbA := sha256.Sum256([]byte("cert-a"))
	thumbB := sha256.Sum256([]byte("cert-b"))

	reg.Add(agentregistry.Entry{
		AgentName:    "clawker.b",
		ContainerID:  "ctr-b",
		Thumbprint:   thumbB,
		RegisteredAt: now,
		LastSeen:     now,
	})
	reg.Add(agentregistry.Entry{
		AgentName:    "clawker.a",
		ContainerID:  "ctr-a",
		Thumbprint:   thumbA,
		RegisteredAt: now.Add(time.Second),
		LastSeen:     now.Add(time.Second),
	})

	srv := &adminServer{agents: reg}
	resp, err := srv.ListAgents(context.Background(), &adminv1.ListAgentsRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Agents, 2)

	// Sorted by AgentName.
	assert.Equal(t, "clawker.a", resp.Agents[0].AgentName)
	assert.Equal(t, "clawker.b", resp.Agents[1].AgentName)

	// Thumbprint hex matches the stored thumbprint exactly.
	assert.Equal(t, hex.EncodeToString(thumbA[:]), resp.Agents[0].CertThumbprint)
	assert.Equal(t, hex.EncodeToString(thumbB[:]), resp.Agents[1].CertThumbprint)

	// Container IDs round-trip.
	assert.Equal(t, "ctr-a", resp.Agents[0].ContainerId)
	assert.Equal(t, "ctr-b", resp.Agents[1].ContainerId)

	// Timestamps round-trip as Unix seconds.
	assert.Equal(t, now.Add(time.Second).Unix(), resp.Agents[0].RegisteredAtUnix)
	assert.Equal(t, now.Unix(), resp.Agents[1].RegisteredAtUnix)
}

// validAnnounceReq builds a default-shape request: every field is set
// to a value the production handler accepts. Tests that exercise
// validation failures derive from this and clobber the field under
// test, so a future schema change forces every case to be revisited.
func validAnnounceReq() *adminv1.AnnounceAgentRequest {
	thumb := sha256.Sum256([]byte("agent-cert"))
	return &adminv1.AnnounceAgentRequest{
		AgentName:              "clawker.alpha.bravo",
		ContainerId:            "ctr-12345",
		ExpectedCertThumbprint: hex.EncodeToString(thumb[:]),
		CodeChallenge:          "challenge-base64url-content",
		CodeChallengeMethod:    string(consts.ChallengeMethodS256),
	}
}

func TestAdminServer_AnnounceAgent_Reserves(t *testing.T) {
	now := time.Unix(2000, 0)
	var reserved []agentslots.Slot
	mock := &slotmocks.RegistryMock{
		ReserveFunc: func(slot agentslots.Slot) error {
			reserved = append(reserved, slot)
			return nil
		},
	}

	srv := &adminServer{slots: mock, clock: func() time.Time { return now }}
	req := validAnnounceReq()
	resp, err := srv.AnnounceAgent(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Returned ExpiresAtUnix matches now + AgentSlotTTL — the CLI uses
	// this for logging only; the CP enforces TTL internally.
	assert.Equal(t, now.Add(consts.AgentSlotTTL).Unix(), resp.ExpiresAtUnix)

	// Reserve was called with a Slot whose CLI-asserted fields match
	// the request body. ReservedAt/ExpiresAt are stamped INSIDE
	// agentslots.Reserve from the registry's own clock — we don't
	// assert them here, the agentslots package has its own tests.
	require.Len(t, reserved, 1)
	got := reserved[0]
	assert.Equal(t, req.AgentName, got.AgentName)
	assert.Equal(t, req.ContainerId, got.ContainerID)
	assert.Equal(t, req.CodeChallenge, got.Challenge)
	assert.Equal(t, consts.ChallengeMethodS256, got.ChallengeMethod)

	// Thumbprint round-trips through hex.
	rawThumb, err := hex.DecodeString(req.ExpectedCertThumbprint)
	require.NoError(t, err)
	var want [sha256.Size]byte
	copy(want[:], rawThumb)
	assert.Equal(t, want, got.ExpectedCertThumbprint)
}

func TestAdminServer_AnnounceAgent_Validation(t *testing.T) {
	mock := &slotmocks.RegistryMock{
		ReserveFunc: func(_ agentslots.Slot) error {
			t.Fatal("Reserve must NOT be called when request fails validation")
			return nil
		},
	}
	srv := &adminServer{slots: mock, clock: func() time.Time { return time.Unix(2000, 0) }}

	cases := []struct {
		name    string
		mutate  func(*adminv1.AnnounceAgentRequest)
		wantMsg string
	}{
		{"nil request", nil, "request required"},
		{"empty agent_name", func(r *adminv1.AnnounceAgentRequest) { r.AgentName = "" }, "agent_name required"},
		{"empty container_id", func(r *adminv1.AnnounceAgentRequest) { r.ContainerId = "" }, "container_id required"},
		{"empty code_challenge", func(r *adminv1.AnnounceAgentRequest) { r.CodeChallenge = "" }, "code_challenge required"},
		{"non-S256 method", func(r *adminv1.AnnounceAgentRequest) { r.CodeChallengeMethod = "plain" }, "code_challenge_method must be S256"},
		{"thumbprint not hex", func(r *adminv1.AnnounceAgentRequest) { r.ExpectedCertThumbprint = "not-hex-zzz" }, "expected_cert_thumbprint must be 64 lowercase hex characters"},
		{"thumbprint wrong length", func(r *adminv1.AnnounceAgentRequest) { r.ExpectedCertThumbprint = "deadbeef" }, "expected_cert_thumbprint must be 64 lowercase hex characters"},
		{"thumbprint uppercase hex", func(r *adminv1.AnnounceAgentRequest) {
			thumb := sha256.Sum256([]byte("agent-cert"))
			// hex.EncodeToString returns lowercase; force uppercase to
			// exercise the case-strictness branch.
			r.ExpectedCertThumbprint = strings.ToUpper(hex.EncodeToString(thumb[:]))
		}, "expected_cert_thumbprint must be 64 lowercase hex characters"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req *adminv1.AnnounceAgentRequest
			if tc.mutate != nil {
				req = validAnnounceReq()
				tc.mutate(req)
			}
			_, err := srv.AnnounceAgent(context.Background(), req)
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Contains(t, status.Convert(err).Message(), tc.wantMsg)
		})
	}
}

// TestAdminServer_AnnounceAgent_SlotExists exercises the duplicate-key
// branch. agentslots returns ErrSlotExists when the same composite
// (cert_thumbprint, agent_name) is already pending; AdminService maps
// that to codes.AlreadyExists so the CLI can surface "agent already
// announced" rather than a generic Internal failure.
func TestAdminServer_AnnounceAgent_SlotExists(t *testing.T) {
	mock := &slotmocks.RegistryMock{
		ReserveFunc: func(_ agentslots.Slot) error { return agentslots.ErrSlotExists },
	}
	srv := &adminServer{slots: mock, clock: func() time.Time { return time.Unix(2000, 0) }}

	_, err := srv.AnnounceAgent(context.Background(), validAnnounceReq())
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
}

func TestAdminServer_AnnounceAgent_ReserveError(t *testing.T) {
	mock := &slotmocks.RegistryMock{
		ReserveFunc: func(_ agentslots.Slot) error { return errors.New("disk full") },
	}
	srv := &adminServer{slots: mock, clock: func() time.Time { return time.Unix(2000, 0) }}

	_, err := srv.AnnounceAgent(context.Background(), validAnnounceReq())
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// TestNewAdminServer_NilSlotsPanics locks the fail-fast contract: a CP
// build that constructs AdminService without a slot registry is a
// wiring regression, not a partial-build state. Mirrors agent.NewHandler's
// panic-on-nil-deps posture so the regression surfaces at startup
// rather than as opaque codes.Internal responses on every announce.
func TestNewAdminServer_NilSlotsPanics(t *testing.T) {
	assert.PanicsWithValue(t,
		"controlplane: NewAdminServer requires non-nil slots registry",
		func() {
			NewAdminServer(nil, nil, nil, nil)
		})
}
