-- +goose Up
-- File đính kèm của task: chỉ thông tin file, nội dung nằm trong storage.
CREATE TABLE attachments (
    id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id      bigint      NOT NULL REFERENCES tasks (id) ON DELETE CASCADE, -- xóa task thì xóa luôn hàng
    name         text        NOT NULL,        -- tên file gốc client gửi, chỉ để hiển thị
    stored_name  text        NOT NULL UNIQUE, -- tên ngẫu nhiên server đặt cho file trong storage
    content_type text        NOT NULL,        -- loại file server tự nhận ra từ nội dung
    size         bigint      NOT NULL CHECK (size > 0),
    uploaded_by  bigint      REFERENCES users (id) ON DELETE SET NULL, -- NULL: user đã bị xóa
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX attachments_task_id_idx ON attachments (task_id);

-- +goose Down
DROP TABLE attachments;
