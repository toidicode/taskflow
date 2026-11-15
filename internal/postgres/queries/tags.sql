-- name: UpsertTags :many
-- Tạo các tag chưa có, trả về id của mọi tag: DO NOTHING không trả về hàng bị trùng
-- nên dùng DO UPDATE (không đổi gì).
INSERT INTO tags (name)
SELECT unnest(@names::text[])
ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
RETURNING id;

-- name: AddTaskTags :exec
INSERT INTO task_tags (task_id, tag_id)
SELECT @task_id, unnest(@tag_ids::bigint[])
ON CONFLICT DO NOTHING;

-- name: DeleteTaskTags :exec
DELETE FROM task_tags
WHERE task_id = $1;

-- name: RemoveTaskTag :execrows
DELETE FROM task_tags
WHERE task_id = $1
  AND tag_id = (SELECT id FROM tags WHERE name = $2);

-- name: ListTaskTags :many
-- Tag của nhiều task trong một câu truy vấn: tránh N+1 khi liệt kê task.
SELECT tt.task_id, tg.name
FROM task_tags tt
JOIN tags tg ON tg.id = tt.tag_id
WHERE tt.task_id = ANY(@task_ids::bigint[])
ORDER BY tt.task_id, tg.name;
