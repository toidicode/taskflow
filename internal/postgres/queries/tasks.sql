-- name: CreateTask :one
INSERT INTO tasks (project_id, title, description, status, assignee_id, due_date, completed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id;

-- name: GetTask :one
SELECT * FROM tasks
WHERE id = $1;

-- name: ListTasks :many
-- Tham số NULL thì bỏ qua điều kiện lọc tương ứng.
SELECT * FROM tasks
WHERE (sqlc.narg(project_id)::bigint IS NULL OR project_id = sqlc.narg(project_id))
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status))
ORDER BY id;

-- name: UpdateTask :execrows
UPDATE tasks
SET project_id = $2, title = $3, description = $4, status = $5,
    assignee_id = $6, due_date = $7, completed_at = $8
WHERE id = $1;

-- name: DeleteTask :execrows
DELETE FROM tasks
WHERE id = $1;
