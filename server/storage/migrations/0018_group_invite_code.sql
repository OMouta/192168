-- The code you send a friend. It replaces having to tell them a group name and
-- a password, and it can be thrown away and made again if it gets somewhere it
-- should not have.
--
-- Nullable only so groups that already exist can be given one on the way up.
-- Nothing reads a group without one after that.
ALTER TABLE groups ADD COLUMN invite_code TEXT;

CREATE UNIQUE INDEX idx_groups_invite_code ON groups(invite_code);
