package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/socketbridge"
)

const (
	grantColumns   = "id, harness_path, harness_name, host_path, status, host_uid, host_gid, host_owner, host_group, purpose, granted_at"
	lookupGrantSQL = "SELECT " + grantColumns + " FROM grants WHERE harness_path = ? AND host_path = ?"
	listGrantsSQL  = "SELECT " + grantColumns + " FROM grants ORDER BY id"
	writeGrantSQL  = `INSERT INTO grants (
    harness_path, harness_name, host_path, status,
    host_uid, host_gid, host_owner, host_group, purpose, granted_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(harness_path, host_path) DO UPDATE SET
    harness_name = excluded.harness_name,
    status = excluded.status,
    host_uid = excluded.host_uid,
    host_gid = excluded.host_gid,
    host_owner = excluded.host_owner,
    host_group = excluded.host_group,
    purpose = excluded.purpose,
    granted_at = excluded.granted_at`
	revokeGrantSQL         = "DELETE FROM grants WHERE id = ?"
	revokeHarnessGrantsSQL = "DELETE FROM grants WHERE harness_path = ?"
	revokeAllGrantsSQL     = "DELETE FROM grants"
	pruneGrantPrefixSQL    = "DELETE FROM grants WHERE harness_path = ? AND host_path NOT IN ("

	eventSocketGrantWrite   = "socket_grant_write"
	eventSocketGrantRevoke  = "socket_grant_revoke"
	eventSocketGrantPrune   = "socket_grant_prune"
	messageSocketGrantWrite = "socket grant stored"
	messageSocketRevoke     = "socket grants revoked"
	messageSocketPrune      = "socket grants pruned"
)

// SocketGrant is one stored socket approval or denial.
type SocketGrant struct {
	ID          int64
	HarnessPath string
	HarnessName string
	HostPath    string
	Status      string
	Identity    socketbridge.ListenerIdentity
	Purpose     string
	GrantedAt   time.Time
}

// SocketGrantStore is the command-layer socket grant store seam.
//
//go:generate moq -rm -pkg mocks -out mocks/grants_mock.go . SocketGrantStore
type SocketGrantStore interface {
	LookupSocketGrant(harnessPath, hostPath string) (*SocketGrant, error)
	GrantSocket(harnessPath, harnessName, hostPath string, declaration config.HarnessSocket, identity socketbridge.ListenerIdentity) error
	DenySocket(harnessPath, harnessName, hostPath string, declaration config.HarnessSocket, identity socketbridge.ListenerIdentity) error
	RevokeSocket(id int64) error
	RevokeHarnessSockets(harnessPath string) error
	RevokeAllSockets() error
	ListSocketGrants() ([]SocketGrant, error)
	PruneHarnessSockets(harnessPath string, declaredHostPaths []string) error
}

type socketGrantStore struct {
	database *DB
}

// NewSocketGrantStore creates the socket grant store for a CLI database.
func NewSocketGrantStore(database *DB) SocketGrantStore {
	return &socketGrantStore{database: database}
}

type rowScanner interface {
	Scan(dest ...any) error
}

// LookupSocketGrant returns the stored row for a harness and host path. It
// returns nil without an error when the pair has no row.
func (s *socketGrantStore) LookupSocketGrant(harnessPath, hostPath string) (*SocketGrant, error) {
	grant, err := scanSocketGrant(s.database.sql.QueryRow(lookupGrantSQL, harnessPath, hostPath))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lookup socket grant: %w", err)
	}
	return grant, nil
}

// GrantSocket stores a persistent approval.
func (s *socketGrantStore) GrantSocket(harnessPath, harnessName, hostPath string, declaration config.HarnessSocket, identity socketbridge.ListenerIdentity) error {
	return s.writeSocketGrant(harnessPath, harnessName, hostPath, GrantAllow, declaration, identity)
}

// DenySocket stores a persistent denial.
func (s *socketGrantStore) DenySocket(harnessPath, harnessName, hostPath string, declaration config.HarnessSocket, identity socketbridge.ListenerIdentity) error {
	return s.writeSocketGrant(harnessPath, harnessName, hostPath, GrantDeny, declaration, identity)
}

func (s *socketGrantStore) writeSocketGrant(harnessPath, harnessName, hostPath, status string, declaration config.HarnessSocket, identity socketbridge.ListenerIdentity) error {
	grantedAt := time.Now().UTC().Truncate(time.Second)
	if _, err := s.database.sql.Exec(
		writeGrantSQL,
		harnessPath,
		harnessName,
		hostPath,
		status,
		identity.UID,
		identity.GID,
		identity.Owner,
		identity.Group,
		declaration.Purpose,
		grantedAt.Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("write socket grant: %w", err)
	}
	s.database.log.Info().
		Str("event", eventSocketGrantWrite).
		Str("harness_path", harnessPath).
		Str("host_path", hostPath).
		Str("status", status).
		Int("host_uid", identity.UID).
		Int("host_gid", identity.GID).
		Str("host_owner", identity.Owner).
		Str("host_group", identity.Group).
		Time("granted_at", grantedAt).
		Msg(messageSocketGrantWrite)
	return nil
}

// RevokeSocket deletes one grant by its user-facing ID.
func (s *socketGrantStore) RevokeSocket(id int64) error {
	return s.deleteAndLog(revokeGrantSQL, id)
}

// RevokeHarnessSockets deletes all rows for one harness principal.
func (s *socketGrantStore) RevokeHarnessSockets(harnessPath string) error {
	return s.deleteAndLog(revokeHarnessGrantsSQL, harnessPath)
}

// RevokeAllSockets deletes all socket grant rows.
func (s *socketGrantStore) RevokeAllSockets() error {
	return s.deleteAndLog(revokeAllGrantsSQL)
}

func (s *socketGrantStore) deleteAndLog(query string, args ...any) error {
	result, err := s.database.sql.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("revoke socket grants: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read revoked socket grant count: %w", err)
	}
	s.database.log.Info().Str("event", eventSocketGrantRevoke).Int64("rows", rows).Msg(messageSocketRevoke)
	return nil
}

// ListSocketGrants returns all rows in grant ID order.
func (s *socketGrantStore) ListSocketGrants() ([]SocketGrant, error) {
	rows, err := s.database.sql.Query(listGrantsSQL)
	if err != nil {
		return nil, fmt.Errorf("list socket grants: %w", err)
	}
	grants := make([]SocketGrant, 0)
	for rows.Next() {
		grant, scanErr := scanSocketGrant(rows)
		if scanErr != nil {
			return nil, closeRowsWithError(rows, fmt.Errorf("scan socket grant: %w", scanErr))
		}
		grants = append(grants, *grant)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, closeRowsWithError(rows, fmt.Errorf("iterate socket grants: %w", rowsErr))
	}
	if closeErr := rows.Close(); closeErr != nil {
		return nil, fmt.Errorf("close socket grant rows: %w", closeErr)
	}
	return grants, nil
}

// PruneHarnessSockets deletes rows for one harness that are not in the
// current resolved declaration list.
func (s *socketGrantStore) PruneHarnessSockets(harnessPath string, declaredHostPaths []string) error {
	if len(declaredHostPaths) == 0 {
		return s.pruneAndLog(revokeHarnessGrantsSQL, harnessPath, harnessPath)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(declaredHostPaths)), ",")
	query := pruneGrantPrefixSQL + placeholders + ")"
	args := make([]any, 0, len(declaredHostPaths)+1)
	args = append(args, harnessPath)
	for _, hostPath := range declaredHostPaths {
		args = append(args, hostPath)
	}
	return s.pruneAndLog(query, harnessPath, args...)
}

func (s *socketGrantStore) pruneAndLog(query, harnessPath string, args ...any) error {
	result, err := s.database.sql.ExecContext(context.Background(), query, args...)
	if err != nil {
		return fmt.Errorf("prune socket grants for %q: %w", harnessPath, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read pruned socket grant count for %q: %w", harnessPath, err)
	}
	s.database.log.Info().
		Str("event", eventSocketGrantPrune).
		Str("harness_path", harnessPath).
		Int64("rows", rows).
		Msg(messageSocketPrune)
	return nil
}

func scanSocketGrant(row rowScanner) (*SocketGrant, error) {
	grant := &SocketGrant{}
	var grantedAt string
	if err := row.Scan(
		&grant.ID,
		&grant.HarnessPath,
		&grant.HarnessName,
		&grant.HostPath,
		&grant.Status,
		&grant.Identity.UID,
		&grant.Identity.GID,
		&grant.Identity.Owner,
		&grant.Identity.Group,
		&grant.Purpose,
		&grantedAt,
	); err != nil {
		return nil, err
	}
	parsedTime, err := time.Parse(time.RFC3339, grantedAt)
	if err != nil {
		return nil, fmt.Errorf("parse granted_at %q: %w", grantedAt, err)
	}
	grant.GrantedAt = parsedTime
	return grant, nil
}

func closeRowsWithError(rows *sql.Rows, err error) error {
	if closeErr := rows.Close(); closeErr != nil {
		return errors.Join(err, fmt.Errorf("close socket grant rows: %w", closeErr))
	}
	return err
}
