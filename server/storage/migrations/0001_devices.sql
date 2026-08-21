CREATE TABLE devices (
	id            TEXT PRIMARY KEY,
	public_key    TEXT NOT NULL UNIQUE,
	transport_key TEXT NOT NULL,
	name          TEXT NOT NULL,
	created_at    INTEGER NOT NULL,
	last_seen_at  INTEGER NOT NULL
);
