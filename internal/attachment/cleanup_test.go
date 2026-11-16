package attachment

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/toidicode/taskflow/internal/storage"
)

func TestCleanOrphans(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir() // go test tự xóa thư mục khi test xong
	files, err := storage.NewDisk(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	store := NewMemoryStore()

	now := time.Now()
	add := func(name string, age time.Duration, withRow bool) {
		t.Helper()
		if _, err := files.Save(ctx, name, strings.NewReader("nội dung")); err != nil {
			t.Fatal(err)
		}
		mtime := now.Add(-age)
		if err := os.Chtimes(filepath.Join(dir, name), mtime, mtime); err != nil { // giả lập file cũ
			t.Fatal(err)
		}
		if withRow {
			if err := store.Add(ctx, &Attachment{TaskID: 1, Name: name, StoredName: name, Size: 8}); err != nil {
				t.Fatal(err)
			}
		}
	}
	add("CO-HANG", 2*time.Hour, true)     // cũ nhưng có hàng: giữ
	add("MO-COI-CU", 2*time.Hour, false)  // cũ, không có hàng: xóa
	add("MO-COI-MOI", time.Minute, false) // không có hàng nhưng mới: có thể đang upload, giữ

	removed, err := CleanOrphans(ctx, store, files, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("xóa %d file, muốn 1", removed)
	}
	list, _ := files.List(ctx)
	var names []string
	for _, f := range list {
		names = append(names, f.Name)
	}
	if want := []string{"CO-HANG", "MO-COI-MOI"}; !slices.Equal(names, want) {
		t.Errorf("còn %v, muốn %v", names, want)
	}
}
