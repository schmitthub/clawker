-- +goose Up
CREATE TABLE grants (
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
);

-- +goose Down
DROP TABLE IF EXISTS grants;
