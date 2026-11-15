-- +goose Up
-- Trước migration này owner_id, assignee_id không có khóa ngoại: dữ liệu cũ có thể trỏ tới user không tồn tại.

-- Task giao cho user không tồn tại: coi như chưa giao cho ai.
UPDATE tasks SET assignee_id = NULL
WHERE assignee_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM users WHERE users.id = tasks.assignee_id);

-- Xóa user thì task của họ trở về "chưa giao".
ALTER TABLE tasks ADD CONSTRAINT tasks_assignee_id_fkey
    FOREIGN KEY (assignee_id) REFERENCES users (id) ON DELETE SET NULL;

-- Project của user không tồn tại: không đoán chủ mới, câu này lỗi và cả migration bị rollback.
-- Không có ON DELETE: không xóa được user còn project.
ALTER TABLE projects ADD CONSTRAINT projects_owner_id_fkey
    FOREIGN KEY (owner_id) REFERENCES users (id);

-- ListTasks, ListProjects lọc theo hai cột này
CREATE INDEX tasks_assignee_id_idx ON tasks (assignee_id);
CREATE INDEX projects_owner_id_idx ON projects (owner_id);

-- +goose Down
DROP INDEX projects_owner_id_idx;
DROP INDEX tasks_assignee_id_idx;
ALTER TABLE projects DROP CONSTRAINT projects_owner_id_fkey;
ALTER TABLE tasks DROP CONSTRAINT tasks_assignee_id_fkey;
