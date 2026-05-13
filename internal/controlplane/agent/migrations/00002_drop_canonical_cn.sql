-- +goose Up
-- AgentFullName lives in a urn:clawker:agent: URI SAN; cert CN is
-- the literal consts.ContainerClawkerd. Trust derives from the
-- kernel-attested peer IP via Docker labels, not from cached
-- strings. The canonical_cn pre-compute column has no readers.
ALTER TABLE agents DROP COLUMN canonical_cn;

-- +goose Down
-- Best-effort restore. Original column was NOT NULL with no default;
-- restoring requires a default so existing rows back-fill cleanly.
-- Rows that survived an up→down round-trip carry an empty
-- canonical_cn — acceptable for alpha rollback.
ALTER TABLE agents ADD COLUMN canonical_cn TEXT NOT NULL DEFAULT '';
