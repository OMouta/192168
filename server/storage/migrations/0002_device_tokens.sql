-- Tokens are stored hashed. A leaked database should not hand out live
-- credentials, and the server never needs the original back.
CREATE TABLE device_tokens (
	token_hash TEXT PRIMARY KEY,
	device_id  TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	created_at INTEGER NOT NULL,
	revoked_at INTEGER
);
