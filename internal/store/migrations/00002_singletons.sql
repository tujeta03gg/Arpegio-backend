-- +goose Up
-- Single-row tables: queue_state and player_state always exist so services can just UPDATE them.
INSERT INTO queue_state (id) VALUES (1);
INSERT INTO player_state (id, updated_at) VALUES (1, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));

-- +goose Down
SELECT 1;
