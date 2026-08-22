-- A nickname belongs to the person, not to each group they are in.
ALTER TABLE devices ADD COLUMN nickname TEXT NOT NULL DEFAULT '';

-- Whatever they last called themselves, falling back to the machine name for a
-- device that never joined anything.
UPDATE devices SET nickname = COALESCE(
	(SELECT m.nickname FROM memberships m
	 WHERE m.device_id = devices.id AND m.nickname != ''
	 ORDER BY m.created_at DESC LIMIT 1),
	name);

ALTER TABLE memberships DROP COLUMN nickname;
