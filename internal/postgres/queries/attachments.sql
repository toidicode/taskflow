-- name: CreateAttachment :one
INSERT INTO attachments (task_id, name, stored_name, content_type, size, uploaded_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, created_at;

-- name: GetAttachment :one
SELECT * FROM attachments
WHERE id = $1;

-- name: ListAttachments :many
SELECT * FROM attachments
WHERE task_id = $1
ORDER BY id;

-- name: DeleteAttachment :execrows
DELETE FROM attachments
WHERE id = $1;
