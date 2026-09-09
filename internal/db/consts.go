package db

const (
	// GrantAllow is a stored persistent approval.
	GrantAllow = "allow"
	// GrantDeny is a stored persistent denial.
	GrantDeny = "deny"

	sqliteDriverName = "sqlite"
	sqliteDSNSuffix  = "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	sqliteWALSuffix  = "-wal"
	sqliteSHMSuffix  = "-shm"
	sqliteFileMode   = 0o600
)
