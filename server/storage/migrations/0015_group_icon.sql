-- A group's own look, so a list of them can be read at a glance rather than
-- word by word. Keys rather than a glyph and a hex colour: the app owns the
-- vocabulary, and a key it does not know falls back to the default.
ALTER TABLE groups ADD COLUMN icon TEXT NOT NULL DEFAULT '';
