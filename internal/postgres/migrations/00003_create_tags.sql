-- +goose Up
-- Tag của task: quan hệ n-n giữa tasks và tags qua bảng nối task_tags.
CREATE TABLE tags (
    id   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text   NOT NULL UNIQUE
);

CREATE TABLE task_tags (
    task_id bigint NOT NULL REFERENCES tasks (id) ON DELETE CASCADE, -- xóa task thì xóa luôn liên kết
    tag_id  bigint NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, tag_id) -- một tag chỉ gắn vào một task một lần
);

-- khóa chính (task_id, tag_id) dùng được khi tìm theo task_id, tìm task theo tag thì cần index riêng
CREATE INDEX task_tags_tag_id_idx ON task_tags (tag_id);

-- +goose Down
DROP TABLE task_tags;
DROP TABLE tags;
