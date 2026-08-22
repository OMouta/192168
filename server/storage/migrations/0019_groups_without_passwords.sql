-- A code is the way into a group now, so the password is gone and with it the
-- reason group names had to be unique. Two sets of friends can both have a
-- Friday Night.
--
-- Rebuilt rather than altered, because both of the things going away were part
-- of the table's own definition.
CREATE TABLE groups_new (
	id                   TEXT PRIMARY KEY,
	name                 TEXT NOT NULL,
	icon                 TEXT NOT NULL DEFAULT '',
	color                TEXT NOT NULL DEFAULT '',
	subnet               TEXT NOT NULL,
	invite_code          TEXT NOT NULL UNIQUE,
	created_by_device_id TEXT NOT NULL REFERENCES devices(id),
	created_at           INTEGER NOT NULL
);

INSERT INTO groups_new (id, name, icon, color, subnet, invite_code, created_by_device_id, created_at)
SELECT id, name, icon, color, subnet, invite_code, created_by_device_id, created_at FROM groups;

DROP TABLE groups;
ALTER TABLE groups_new RENAME TO groups;
