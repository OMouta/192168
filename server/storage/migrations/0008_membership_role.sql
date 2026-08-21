-- Who may manage a group. On the membership rather than the group, so it
-- generalises to more than one person and survives being handed over without
-- touching the group itself.
ALTER TABLE memberships ADD COLUMN role TEXT NOT NULL DEFAULT 'member';
