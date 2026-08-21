-- An address belongs to a membership rather than to a session. It is given at
-- the door and does not change, so a host keeps the address their friends
-- already typed into a game.
ALTER TABLE memberships ADD COLUMN virtual_ip TEXT;
