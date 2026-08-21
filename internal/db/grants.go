package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
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

// ErrGrantNotFound means that no socket grant matched a lookup.
var ErrGrantNotFound = errors.New("socket grant not found")

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
	LookupSocketGrant(ctx context.Context, harnessPath, hostPath string) (*SocketGrant, error)
	GrantSocket(
		ctx context.Context,
		harnessPath, harnessName, hostPath string,
		declaration config.HarnessSocket,
		identity socketbridge.ListenerIdentity,
	) error
	DenySocket(
		ctx context.Context,
		harnessPath, harnessName, hostPath string,
		declaration config.HarnessSocket,
		identity socketbridge.ListenerIdentity,
	) error
	RevokeSocket(ctx context.Context, id int64) (int64, error)
	RevokeHarnessSockets(ctx context.Context, harnessPath string) (int64, error)
	RevokeAllSockets(ctx context.Context) (int64, error)
	ListSocketGrants(ctx context.Context) ([]SocketGrant, error)
	PruneHarnessSockets(ctx context.Context, harnessPath string, declaredHostPaths []string) error
}

// SocketGrantSQLStore stores socket grant decisions in the CLI database.
type SocketGrantSQLStore struct {
	database *DB
	log      *logger.Logger
}

// NewSocketGrantStore creates the socket grant store for a CLI database.
func NewSocketGrantStore(
	database *DB,
	log *logger.Logger,
) *SocketGrantSQLStore {
	if log == nil {
		log = logger.Nop()
	}
	return &SocketGrantSQLStore{database: database, log: log}
}

type rowScanner interface {
	Scan(dest ...any) error
}

// LookupSocketGrant returns the stored row for a harness and host path.
func (s *SocketGrantSQLStore) LookupSocketGrant(
	ctx context.Context,
	harnessPath, hostPath string,
) (*SocketGrant, error) {
	grant, err := scanSocketGrant(s.database.sql.QueryRowContext(
		ctx,
		lookupGrantSQL,
		harnessPath,
		hostPath,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("lookup socket grant: %w", ErrGrantNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("lookup socket grant: %w", err)
	}
	return grant, nil
}

// GrantSocket stores a persistent approval.
func (s *SocketGrantSQLStore) GrantSocket(
	ctx context.Context,
	harnessPath, harnessName, hostPath string,
	declaration config.HarnessSocket,
	identity socketbridge.ListenerIdentity,
) error {
	return s.writeSocketGrant(ctx, harnessPath, harnessName, hostPath, GrantAllow, declaration, identity)
}

// DenySocket stores a persistent denial.
func (s *SocketGrantSQLStore) DenySocket(
	ctx context.Context,
	harnessPath, harnessName, hostPath string,
	declaration config.HarnessSocket,
	identity socketbridge.ListenerIdentity,
) error {
	return s.writeSocketGrant(ctx, harnessPath, harnessName, hostPath, GrantDeny, declaration, identity)
}

func (s *SocketGrantSQLStore) writeSocketGrant(
	ctx context.Context,
	harnessPath, harnessName, hostPath, status string,
	declaration config.HarnessSocket,
	identity socketbridge.ListenerIdentity,
) error {
	grantedAt := time.Now().UTC().Truncate(time.Second)
	if _, err := s.database.sql.ExecContext(
		ctx,
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
	s.log.Info().
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
func (s *SocketGrantSQLStore) RevokeSocket(ctx context.Context, id int64) (int64, error) {
	rows, err := s.deleteRows(ctx, revokeGrantSQL, id)
	if err != nil {
		return 0, err
	}
	s.log.Info().
		Str("event", eventSocketGrantRevoke).
		Int64("grant_id", id).
		Int64("rows", rows).
		Msg(messageSocketRevoke)
	return rows, nil
}

// RevokeHarnessSockets deletes all rows for one harness principal.
func (s *SocketGrantSQLStore) RevokeHarnessSockets(ctx context.Context, harnessPath string) (int64, error) {
	rows, err := s.deleteRows(ctx, revokeHarnessGrantsSQL, harnessPath)
	if err != nil {
		return 0, err
	}
	s.log.Info().
		Str("event", eventSocketGrantRevoke).
		Str("harness_path", harnessPath).
		Int64("rows", rows).
		Msg(messageSocketRevoke)
	return rows, nil
}

// RevokeAllSockets deletes all socket grant rows.
func (s *SocketGrantSQLStore) RevokeAllSockets(ctx context.Context) (int64, error) {
	rows, err := s.deleteRows(ctx, revokeAllGrantsSQL)
	if err != nil {
		return 0, err
	}
	s.log.Info().
		Str("event", eventSocketGrantRevoke).
		Str("scope", "all").
		Int64("rows", rows).
		Msg(messageSocketRevoke)
	return rows, nil
}

func (s *SocketGrantSQLStore) deleteRows(ctx context.Context, query string, args ...any) (int64, error) {
	result, err := s.database.sql.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("revoke socket grants: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read revoked socket grant count: %w", err)
	}
	return rows, nil
}

// ListSocketGrants returns all rows in grant ID order.
func (s *SocketGrantSQLStore) ListSocketGrants(ctx context.Context) ([]SocketGrant, error) {
	rows, err := s.database.sql.QueryContext(ctx, listGrantsSQL)
	if err != nil {
		return nil, fmt.Errorf("list socket grants: %w", err)
	}
	defer func() {
		// Each return below closes the rows and reports the close error. This
		// deferred close is only a safety action for an unexpected panic.
		_ = rows.Close()
	}()

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
func (s *SocketGrantSQLStore) PruneHarnessSockets(
	ctx context.Context,
	harnessPath string,
	declaredHostPaths []string,
) error {
	if len(declaredHostPaths) == 0 {
		return s.pruneAndLog(ctx, revokeHarnessGrantsSQL, harnessPath, harnessPath)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(declaredHostPaths)), ",")
	query := pruneGrantPrefixSQL + placeholders + ")"
	args := make([]any, 0, len(declaredHostPaths)+1)
	args = append(args, harnessPath)
	for _, hostPath := range declaredHostPaths {
		args = append(args, hostPath)
	}
	return s.pruneAndLog(ctx, query, harnessPath, args...)
}

func (s *SocketGrantSQLStore) pruneAndLog(
	ctx context.Context,
	query, harnessPath string,
	args ...any,
) error {
	result, err := s.database.sql.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("prune socket grants for %q: %w", harnessPath, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read pruned socket grant count for %q: %w", harnessPath, err)
	}
	s.log.Info().
		Str("event", eventSocketGrantPrune).
		Str("harness_path", harnessPath).
		Int64("rows", rows).
		Msg(messageSocketPrune)
	return nil
}

func scanSocketGrant(row rowScanner) (*SocketGrant, error) {
	var grant SocketGrant
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
		return nil, fmt.Errorf("scan socket grant row: %w", err)
	}
	parsedTime, err := time.Parse(time.RFC3339, grantedAt)
	if err != nil {
		return nil, fmt.Errorf("parse granted_at %q: %w", grantedAt, err)
	}
	grant.GrantedAt = parsedTime
	return &grant, nil
}

func closeRowsWithError(rows *sql.Rows, resultErr error) error {
	if closeErr := rows.Close(); closeErr != nil {
		return errors.Join(resultErr, fmt.Errorf("close socket grant rows: %w", closeErr))
	}
	return resultErr
}
