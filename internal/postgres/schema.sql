-- Schema tạm thời của TaskFlow, chạy bằng psql. Bài 30 thay file này bằng migration của goose.

CREATE TABLE IF NOT EXISTS projects (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        text   NOT NULL,
    description text   NOT NULL DEFAULT '',
    owner_id    bigint NOT NULL -- chưa có bảng users nên chưa có khóa ngoại
);

CREATE TABLE IF NOT EXISTS tasks (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id   bigint NOT NULL REFERENCES projects (id),
    title        text   NOT NULL,
    description  text   NOT NULL DEFAULT '',
    status       text   NOT NULL DEFAULT 'todo' CHECK (status IN ('todo', 'doing', 'done')),
    assignee_id  bigint,      -- NULL: chưa giao cho ai
    due_date     timestamptz, -- NULL: không có hạn
    completed_at timestamptz  -- NULL: chưa xong
);

-- PostgreSQL không tự tạo index cho khóa ngoại, mà task hay được lọc theo project
CREATE INDEX IF NOT EXISTS tasks_project_id_idx ON tasks (project_id);
