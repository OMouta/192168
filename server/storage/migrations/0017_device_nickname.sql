-- A nickname belongs to the person, not to each group they are in. Being one
-- name in one group and another somewhere else was work for the user and told
-- nobody anything.
ALTER TABLE devices ADD COLUMN nickname TEXT NOT NULL DEFAULT '';

-- Whatever they last called themselves, which is the closest thing to a name
-- they chose. A device that never joined anything falls back to the machine
-- name it registered with.
UPDATE devices SET nickname = COALESCE(
	(SELECT m.nickname FROM memberships m
	 WHERE m.device_id = devices.id AND m.nickname != ''
	 ORDER BY m.created_at DESC LIMIT 1),
	name);

ALTER TABLE memberships DROP COLUMN nickname;
