-- Set apart from revoked_at, which only means not currently a member. Leaving
-- is revoked and can be undone by joining again; being removed is both, and
-- joining again does not undo it. Without the difference, removing somebody who
-- knows the password does nothing at all.
ALTER TABLE memberships ADD COLUMN banned_at INTEGER;
