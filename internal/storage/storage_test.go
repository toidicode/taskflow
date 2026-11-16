package storage

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDisk(t *testing.T) {
	ctx := context.Background()
	d, err := NewDisk(filepath.Join(t.TempDir(), "uploads")) // thư mục chưa có: NewDisk tạo
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	n, err := d.Save(ctx, "abc", strings.NewReader("xin chào"))
	if err != nil || n != int64(len("xin chào")) {
		t.Fatalf("Save() = %d, %v", n, err)
	}
	// tên đã có: không ghi đè
	if _, err := d.Save(ctx, "abc", strings.NewReader("khác")); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Save() tên đã có: %v, muốn fs.ErrExist", err)
	}

	f, err := d.Open(ctx, "abc")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(f)
	f.Close()
	if string(data) != "xin chào" {
		t.Errorf("nội dung = %q", data)
	}

	if err := d.Remove(ctx, "abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Open(ctx, "abc"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open() sau Remove: %v, muốn fs.ErrNotExist", err)
	}
	if err := d.Remove(ctx, "abc"); err != nil { // xóa lần hai: không lỗi
		t.Errorf("Remove() lần hai: %v", err)
	}
}

// TestDiskStaysInsideRoot: tên file trỏ ra ngoài thư mục đều bị os.Root từ chối.
func TestDiskStaysInsideRoot(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	dir := filepath.Join(parent, "uploads")
	d, err := NewDisk(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	secret := filepath.Join(parent, "secret.txt")
	if err := os.WriteFile(secret, []byte("bí mật"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"../secret.txt", "../hacked.txt", secret, "link"} {
		if _, err := d.Open(ctx, name); err == nil {
			t.Errorf("Open(%q) đọc được file ngoài thư mục", name)
		}
		if _, err := d.Save(ctx, name, strings.NewReader("x")); err == nil {
			t.Errorf("Save(%q) ghi được file ngoài thư mục", name)
		}
	}
	if _, err := os.Stat(filepath.Join(parent, "hacked.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("có file hacked.txt ngoài thư mục: %v", err)
	}
	if data, _ := os.ReadFile(secret); string(data) != "bí mật" {
		t.Errorf("secret.txt bị ghi đè: %q", data)
	}
}

// errReader trả về vài byte rồi lỗi, giống client ngắt kết nối giữa lúc upload.
type errReader struct{ sent bool }

func (r *errReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, errors.New("mất kết nối")
	}
	r.sent = true
	return copy(p, "một phần"), nil
}

func TestDiskSaveErrorLeavesNoFile(t *testing.T) {
	dir := t.TempDir()
	d, err := NewDisk(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Save(context.Background(), "abc", &errReader{}); err == nil {
		t.Fatal("Save() không trả lỗi của reader")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("còn %d file sau khi Save lỗi", len(entries))
	}
}

func TestDiskList(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := NewDisk(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, name := range []string{"b", "a"} {
		if _, err := d.Save(ctx, name, strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
	}
	os.Mkdir(filepath.Join(dir, "con"), 0o750) // thư mục con không phải file đính kèm
	files, err := d.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Name != "a" || files[1].Name != "b" || files[0].ModTime.IsZero() {
		t.Errorf("List() = %v, muốn hai file a, b", files)
	}
}
