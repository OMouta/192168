-- Sessions no longer carry an address. Rebuilt rather than altered because the
-- address was half of a table constraint. Nothing is lost, since a session
-- lasts only as long as somebody is connected.
DROP TABLE sessions;
