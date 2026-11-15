-- name: CreateProject :one
INSERT INTO projects (name, description, owner_id)
VALUES ($1, $2, $3)
RETURNING id;

-- name: GetProject :one
SELECT * FROM projects
WHERE id = $1;

-- name: ListProjects :many
-- Chỉ project của một user: không có truy vấn nào liệt kê project của mọi người.
SELECT * FROM projects
WHERE owner_id = $1
ORDER BY id;

-- name: UpdateProject :execrows
UPDATE projects
SET name = $2, description = $3, owner_id = $4
WHERE id = $1;

-- name: DeleteProject :execrows
DELETE FROM projects
WHERE id = $1;
