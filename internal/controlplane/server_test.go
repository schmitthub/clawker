package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/controlplane/agent"
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
	reg := agent.NewRegistry(nil)
	now := time.Unix(1000, 0)

	thumbA := sha256.Sum256([]byte("cert-a"))
	thumbB := sha256.Sum256([]byte("cert-b"))

	require.NoError(t, reg.Add(agent.Entry{
		AgentName:    "b",
		Project:      "p",
		ContainerID:  "ctr-b",
		Thumbprint:   thumbB,
		RegisteredAt: now,
		LastSeen:     now,
	}))
	require.NoError(t, reg.Add(agent.Entry{
		AgentName:    "a",
		Project:      "p",
		ContainerID:  "ctr-a",
		Thumbprint:   thumbA,
		RegisteredAt: now.Add(time.Second),
		LastSeen:     now.Add(time.Second),
	}))

	srv := &adminServer{agents: reg}
	resp, err := srv.ListAgents(context.Background(), &adminv1.ListAgentsRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Agents, 2)

	// Sorted by (Project, AgentName).
	assert.Equal(t, "a", resp.Agents[0].AgentName)
	assert.Equal(t, "b", resp.Agents[1].AgentName)
	assert.Equal(t, "p", resp.Agents[0].Project)
	assert.Equal(t, "p", resp.Agents[1].Project)

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
