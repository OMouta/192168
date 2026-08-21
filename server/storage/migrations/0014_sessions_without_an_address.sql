CREATE TABLE sessions (
	id               TEXT PRIMARY KEY,
	group_id         TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
	membership_id    TEXT NOT NULL UNIQUE REFERENCES memberships(id) ON DELETE CASCADE,
	endpoint_address TEXT,
	endpoint_port    INTEGER,
	connected_at     INTEGER NOT NULL,
	last_seen_at     INTEGER NOT NULL
);
