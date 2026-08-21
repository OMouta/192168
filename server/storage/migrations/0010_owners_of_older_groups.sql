-- Groups that existed before roles did. Whoever made one owns it.
UPDATE memberships SET role = 'owner'
WHERE EXISTS (
	SELECT 1 FROM groups g
	WHERE g.id = memberships.group_id AND g.created_by_device_id = memberships.device_id
);
