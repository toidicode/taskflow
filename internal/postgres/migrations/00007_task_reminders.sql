-- +goose Up
-- Hạn (due_date) mà task đã được nhắc: job nhắc deadline (Bài 47) chỉ nhắc task có
-- reminded_due_date khác due_date, nên mỗi hạn nhắc một lần và đổi hạn thì nhắc lại.
-- Chỉ thêm cột cho phép NULL: code cũ vẫn chạy được trong lúc deploy (expand).
ALTER TABLE tasks ADD COLUMN reminded_due_date timestamptz;

-- Job nhắc tìm task chưa xong theo hạn
CREATE INDEX tasks_due_date_idx ON tasks (due_date) WHERE status <> 'done';

-- +goose Down
DROP INDEX tasks_due_date_idx;
ALTER TABLE tasks DROP COLUMN reminded_due_date;
