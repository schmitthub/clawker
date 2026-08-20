package db

const (
	// GrantAllow is a stored persistent approval.
	GrantAllow = "allow"
	// GrantDeny is a stored persistent denial.
	GrantDeny = "deny"

	sqliteDriverName = "sqlite"
	sqliteDSNSuffix  = "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	schemaVersion    = 1

	createGrantsTableSQL = `CREATE TABLE grants (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    harness_path TEXT NOT NULL,
    harness_name TEXT NOT NULL,
    host_path    TEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('allow','deny')),
    host_uid     INTEGER NOT NULL,
    host_gid     INTEGER NOT NULL,
    host_owner   TEXT NOT NULL,
    host_group   TEXT NOT NULL,
    purpose      TEXT NOT NULL,
    granted_at   TEXT NOT NULL,
    UNIQUE (harness_path, host_path)
)`
	readSchemaVersionSQL = "PRAGMA user_version"
	setSchemaVersionSQL  = "PRAGMA user_version = 1"
)
