-- Partial, so a revoked membership frees its address without losing it.
-- Somebody who leaves and comes back takes the same one again unless another
-- member claimed it while they were gone.
CREATE UNIQUE INDEX idx_memberships_address
ON memberships(group_id, virtual_ip) WHERE revoked_at IS NULL;
