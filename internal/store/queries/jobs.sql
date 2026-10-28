-- name: InsertJob :exec
INSERT INTO jobs (id, kind, payload, status, run_after) VALUES (?, ?, ?, 'pending', ?);

-- name: NextReadyJob :one
SELECT * FROM jobs WHERE status = 'pending' AND run_after <= ? ORDER BY run_after, id LIMIT 1;

-- name: StartJob :exec
UPDATE jobs SET status = 'running', attempts = attempts + 1 WHERE id = ?;

-- name: FinishJob :exec
UPDATE jobs SET status = ?, run_after = ?, last_error = ? WHERE id = ?;

-- name: ResetRunningJobs :exec
UPDATE jobs SET status = 'pending' WHERE status = 'running';

-- name: NextJobTime :one
SELECT CAST(COALESCE(MIN(run_after), '') AS TEXT) AS next FROM jobs WHERE status = 'pending';

-- name: CountOpenJobs :one
SELECT COUNT(*) FROM jobs WHERE status IN ('pending', 'running');

-- name: CountFailedJobs :one
SELECT COUNT(*) FROM jobs WHERE status = 'failed';

-- name: PruneDoneJobs :exec
DELETE FROM jobs WHERE status = 'done';
