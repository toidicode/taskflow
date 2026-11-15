-- +goose Up
-- TaskFlow coi email không phân biệt hoa thường: app lưu email ở dạng chữ thường
-- (user.NormalizeEmail), CHECK chặn email có chữ hoa đi đường khác vào. Nhờ vậy UNIQUE của cột email
-- (migration 00002) cũng chặn An@Example.com khi đã có an@example.com.
ALTER TABLE users ADD CONSTRAINT users_email_lowercase CHECK (email = lower(email));

-- +goose Down
ALTER TABLE users DROP CONSTRAINT users_email_lowercase;
