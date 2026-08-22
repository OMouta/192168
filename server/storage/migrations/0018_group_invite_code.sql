-- The code you send a friend, in place of a group name and a password.
ALTER TABLE groups ADD COLUMN invite_code TEXT;

-- Groups that predate codes get one here. Eight hex characters is the same
-- shape and the same 32 bits as a minted code, and hex has no 0/O or 1/l to
-- misread, so SQLite can draw it.
UPDATE groups SET invite_code = lower(hex(randomblob(4))) WHERE invite_code IS NULL;

CREATE UNIQUE INDEX idx_groups_invite_code ON groups(invite_code);
