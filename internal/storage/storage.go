// Package storage lưu nội dung file upload. Disk lưu trong một thư mục trên ổ đĩa;
// muốn chuyển sang S3, R2... chỉ cần viết thêm một kiểu cài interface Storage.
package storage

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
)

// Storage là nơi lưu nội dung file theo tên. Tên do server đặt (VD rand.Text()),
// không bao giờ lấy từ client.
type Storage interface {
	// Save ghi nội dung đọc từ r vào file name chưa tồn tại, trả về số byte đã ghi.
	// Lỗi giữa chừng (kể cả lỗi khi đọc r) thì không để lại file nào.
	Save(ctx context.Context, name string, r io.Reader) (int64, error)
	// Open mở file name để đọc, lỗi wrap fs.ErrNotExist khi không có file đó.
	Open(ctx context.Context, name string) (io.ReadSeekCloser, error)
	// Remove xóa file name, không có file đó thì không lỗi.
	Remove(ctx context.Context, name string) error
}

// Disk lưu file trong một thư mục. Mọi thao tác đi qua os.Root nên tên file nào
// (VD "../../etc/passwd" hay symlink trỏ ra ngoài) cũng không thoát ra khỏi thư mục đó.
type Disk struct {
	root *os.Root
}

var _ Storage = (*Disk)(nil)

// NewDisk tạo thư mục dir (nếu chưa có) và trả về Disk lưu file trong đó.
func NewDisk(dir string) (*Disk, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &Disk{root: root}, nil
}

func (d *Disk) Save(ctx context.Context, name string, r io.Reader) (int64, error) {
	// O_EXCL: không bao giờ ghi đè một file đã có
	f, err := d.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	if closeErr := f.Close(); err == nil {
		err = closeErr // ổ đĩa đầy có thể chỉ lộ ra lúc Close
	}
	if err != nil {
		d.root.Remove(name) // không để lại file dở dang
		return n, err
	}
	return n, nil
}

func (d *Disk) Open(ctx context.Context, name string) (io.ReadSeekCloser, error) {
	return d.root.Open(name) // *os.File có Read, Seek, Close
}

func (d *Disk) Remove(ctx context.Context, name string) error {
	if err := d.root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Close đóng thư mục gốc, sau đó không dùng d được nữa.
func (d *Disk) Close() error {
	return d.root.Close()
}
