package attachment

import (
	"context"

	"github.com/toidicode/taskflow/internal/reqlog"
	"github.com/toidicode/taskflow/internal/storage"
)

// Files gom thông tin file (Store) và nội dung file (Storage) của các file đính kèm.
// Nó cài interface task.Files để task.Service dọn file khi xóa task.
type Files struct {
	Store   Store
	Storage storage.Storage
}

// StoredNames trả về tên trong storage của các file đính kèm của task taskID.
func (f Files) StoredNames(ctx context.Context, taskID int) ([]string, error) {
	list, err := f.Store.List(ctx, taskID)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(list))
	for i, a := range list {
		names[i] = a.StoredName
	}
	return names, nil
}

// Remove xóa nội dung các file names khỏi storage. Lỗi chỉ được ghi log: thông tin file trong
// database đã bị xóa (hoặc chưa từng có), file sót lại chỉ tốn chỗ, không ai tải được.
func (f Files) Remove(ctx context.Context, names ...string) {
	ctx = context.WithoutCancel(ctx) // client ngắt kết nối thì vẫn phải dọn file
	for _, name := range names {
		if err := f.Storage.Remove(ctx, name); err != nil {
			reqlog.Logger(ctx).Error("xóa file đính kèm", "stored_name", name, "err", err)
		}
	}
}
