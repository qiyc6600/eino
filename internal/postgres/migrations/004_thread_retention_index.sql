-- Index for thread retention sweeps.
--
-- PruneThreadsBefore deletes a user's threads whose updated_at is older than the
-- configured retention. Without this index that DELETE scans the whole table on
-- every thread write, because the sweep runs on the write path (matching how run
-- events expire).
CREATE INDEX IF NOT EXISTS agent_threads_user_updated_idx
    ON agent_threads(user_id, updated_at);
