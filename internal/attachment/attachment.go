// Package attachment quản lý file đính kèm của task. Store chỉ giữ thông tin của file,
// nội dung file nằm trong một storage.Storage riêng.
package attachment

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Attachment là một file đính kèm của task.
type Attachment struct {
	ID          int       `json:"id"`
	TaskID      int       `json:"task_id"`
	Name        string    `json:"name"`                 // tên file gốc client gửi, chỉ để hiển thị và đặt tên khi tải về
	StoredName  string    `json:"-"`                    // tên ngẫu nhiên trong storage, client không cần biết
	ContentType string    `json:"content_type"`         // loại file server nhận ra từ nội dung, không phải loại client khai
	Size        int64     `json:"size"`                 // byte
	UploadedBy  int       `json:"uploaded_by,omitzero"` // 0: user đã bị xóa
	CreatedAt   time.Time `json:"created_at"`
}

// maxNameBytes là độ dài tối đa của Name, cũng là giới hạn tên file của đa số hệ điều hành.
const maxNameBytes = 255

// CleanName làm sạch tên file client gửi để lưu vào Name: thay ký tự điều khiển, / và \ bằng _,
// bỏ khoảng trắng hai đầu, cắt còn tối đa 255 byte. Tên rỗng, "." hoặc ".." thành "file".
// Kết quả chỉ để hiển thị, không bao giờ được dùng làm đường dẫn.
func CleanName(name string) string {
	name = strings.ToValidUTF8(name, "")
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	for len(name) > maxNameBytes { // cắt từng rune để không làm hỏng chữ có dấu
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	return name
}
