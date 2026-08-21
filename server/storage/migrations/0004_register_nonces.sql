-- Registration nonces, kept only long enough to outlive the timestamp skew a
-- registration is allowed, then pruned.
CREATE TABLE register_nonces (
	nonce   TEXT PRIMARY KEY,
	seen_at INTEGER NOT NULL
);
