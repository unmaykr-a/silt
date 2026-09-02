-- +goose Up

-- Why a container is not running.
--
-- Silt recorded `state` and `health` and treated everything that was not
-- "running" as one thing, which meant a container someone deliberately stopped
-- and a container that was OOM-killed at 03:00 looked identical on every
-- screen. They are the two ends of the range of things worth knowing.
--
-- exit_code is NULL for a running container rather than 0. Docker reports the
-- previous run's code while a container is up, and a stale 0 presented as the
-- current state is worse than presenting nothing: it reads as "exited cleanly"
-- about something that is running fine.
--
-- oom_killed earns its own column because it is not derivable from the code.
-- An OOM kill and a plain `docker kill` are both 137, and only one of them is
-- a memory limit to go and raise.
ALTER TABLE service_states ADD COLUMN exit_code INTEGER;
ALTER TABLE service_states ADD COLUMN oom_killed INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE service_states DROP COLUMN oom_killed;
ALTER TABLE service_states DROP COLUMN exit_code;
