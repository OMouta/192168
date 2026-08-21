CREATE TABLE memberships (
	id         TEXT PRIMARY KEY,
	group_id   TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
	device_id  TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	nickname   TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	revoked_at INTEGER,
	UNIQUE (group_id, device_id)
);
