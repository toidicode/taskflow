package attachment

import (
	"context"
	"errors"
	"time"

	"github.com/toidicode/taskflow/internal/storage"
)

// CleanOrphans xóa các file mồ côi trong files: file không có hàng nào trong store trỏ tới
// và được ghi lần cuối trước before. File mới hơn có thể đang được upload (file được ghi
// xong rồi hàng mới được thêm) nên không bị đụng tới. Trả về số file đã xóa.
func CleanOrphans(ctx context.Context, store Store, files storage.Storage, before time.Time) (int, error) {
	list, err := files.List(ctx)
	if err != nil {
		return 0, err
	}
	var old []string
	for _, f := range list {
		if f.ModTime.Before(before) {
			old = append(old, f.Name)
		}
	}
	if len(old) == 0 {
		return 0, nil
	}
	known, err := store.KnownFiles(ctx, old)
	if err != nil {
		return 0, err
	}
	removed := 0
	var errs []error
	for _, name := range old {
		if known[name] {
			continue
		}
		if err := ctx.Err(); err != nil { // hết thời gian: dừng, lần sau làm tiếp
			return removed, err
		}
		if err := files.Remove(ctx, name); err != nil {
			errs = append(errs, err) // một file lỗi không chặn các file còn lại
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}
