CREATE TABLE groups (
	id                   TEXT PRIMARY KEY,
	name                 TEXT NOT NULL,
	name_normalized      TEXT NOT NULL UNIQUE,
	password_verifier    TEXT NOT NULL,
	subnet               TEXT NOT NULL,
	created_by_device_id TEXT NOT NULL REFERENCES devices(id),
	created_at           INTEGER NOT NULL
);
