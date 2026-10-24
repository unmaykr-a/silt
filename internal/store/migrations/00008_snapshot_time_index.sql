-- +goose Up
-- An index on taken_at alone, for the timeline across every project.
--
-- snapshots had one index covering time, (project_id, taken_at DESC), which a
-- range on taken_at with no project can only use as a skip-scan: SQLite walks
-- every project's slice of the index to merge them back into time order. That
-- is the query behind the default home screen, so the cost was paid on every
-- page load and it grew with the number of projects, which is the dimension a
-- host full of stacks grows in.
--
-- Measured on 60,000 snapshots across 47 projects: a 30-day window went from
-- 20.4ms to 4.3ms, and a 7-day window from 9.7ms to 3.6ms. The index costs
-- about 15 bytes per snapshot row -- under 3% of that database.
--
-- The composite index stays: a project-scoped range still wants project_id as
-- its leading column, and with both present SQLite picks correctly for each.
CREATE INDEX idx_snapshots_time ON snapshots(taken_at DESC);

-- +goose Down
DROP INDEX idx_snapshots_time;
