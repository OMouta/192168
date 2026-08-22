-- The code you send a friend. It replaces having to tell them a group name and
-- a password, and it can be thrown away and made again if it reaches somebody
-- it should not have.
ALTER TABLE groups ADD COLUMN invite_code TEXT;

-- Groups that predate codes get one here. Eight hex characters, which is the
-- same shape and the same amount of randomness as a minted one; there is no
-- 0/O or 1/l in hex to misread, so nothing is lost by letting SQLite draw it.
UPDATE groups SET invite_code = lower(hex(randomblob(4))) WHERE invite_code IS NULL;

CREATE UNIQUE INDEX idx_groups_invite_code ON groups(invite_code);
