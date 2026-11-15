-- name: CreateTask :one
INSERT INTO tasks (project_id, title, description, status, assignee_id, due_date, completed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id;

-- name: GetTask :one
SELECT * FROM tasks
WHERE id = $1;

-- name: ListTasks :many
-- Tham số NULL thì bỏ qua điều kiện lọc tương ứng, max_rows NULL là không giới hạn.
-- Phân trang theo cursor: chỉ lấy task có id lớn hơn after_id (id cuối của trang trước).
SELECT * FROM tasks
WHERE (sqlc.narg(project_id)::bigint IS NULL OR project_id = sqlc.narg(project_id))
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status))
  AND (sqlc.narg(tag)::text IS NULL OR EXISTS (
        SELECT 1 FROM task_tags tt JOIN tags tg ON tg.id = tt.tag_id
        WHERE tt.task_id = tasks.id AND tg.name = sqlc.narg(tag)))
  AND tasks.id > @after_id
  -- visible_to: chỉ task mà user được xem, cùng luật với task.CanView (chủ project hoặc người được giao)
  AND (sqlc.narg(visible_to)::bigint IS NULL
       OR tasks.assignee_id = sqlc.narg(visible_to)
       OR EXISTS (SELECT 1 FROM projects p
                  WHERE p.id = tasks.project_id AND p.owner_id = sqlc.narg(visible_to)))
ORDER BY tasks.id
LIMIT sqlc.narg(max_rows);

-- name: UpdateTask :execrows
UPDATE tasks
SET project_id = $2, title = $3, description = $4, status = $5,
    assignee_id = $6, due_date = $7, completed_at = $8
WHERE id = $1;

-- name: DeleteTask :execrows
DELETE FROM tasks
WHERE id = $1;
