-- +goose Up
CREATE TABLE IF NOT EXISTS agents (
  thumbprint_hex TEXT NOT NULL,
  container_id   TEXT NOT NULL,
  agent_name     TEXT NOT NULL,
  project        TEXT NOT NULL,
  canonical_cn   TEXT NOT NULL,
  registered_at  INTEGER NOT NULL,
  last_seen      INTEGER NOT NULL,
  PRIMARY KEY (thumbprint_hex, container_id),
  UNIQUE (thumbprint_hex),
  UNIQUE (container_id)
);
CREATE INDEX IF NOT EXISTS idx_name_project ON agents(project, agent_name);

-- +goose Down
DROP INDEX IF EXISTS idx_name_project;
DROP TABLE IF EXISTS agents;
