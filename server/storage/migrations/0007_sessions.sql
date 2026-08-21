-- One session per membership, so connecting twice replaces the old one rather
-- than leaving a ghost in the peer list. Virtual IPs are unique inside a group
-- and only for as long as the session lives.
CREATE TABLE sessions (
	id               TEXT PRIMARY KEY,
	group_id         TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
	membership_id    TEXT NOT NULL UNIQUE REFERENCES memberships(id) ON DELETE CASCADE,
	virtual_ip       TEXT NOT NULL,
	endpoint_address TEXT,
	endpoint_port    INTEGER,
	connected_at     INTEGER NOT NULL,
	last_seen_at     INTEGER NOT NULL,
	UNIQUE (group_id, virtual_ip)
);
