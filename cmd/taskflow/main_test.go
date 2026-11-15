package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRun(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tasks.json")
	now := time.Date(2026, 11, 15, 9, 0, 0, 0, time.Local)

	// các case chạy lần lượt trên cùng một file, case sau thấy task của case trước
	tests := []struct {
		name     string
		args     []string
		stdin    string
		wantCode int
		wantOut  string // stdout phải chứa chuỗi này
		wantErr  string // stderr phải chứa chuỗi này
	}{
		{name: "add", args: []string{"add", "-project", "1", "-title", "Thiết kế database", "-due", "2026-11-10"},
			wantOut: "Đã thêm task #1: Thiết kế database"},
		{name: "import", args: []string{"import", "-project", "1"}, stdin: "Viết API\n\n# ghi chú\nViết tài liệu\n",
			wantOut: "Đã thêm 2 task từ stdin"},
		{name: "start", args: []string{"start", "2"}, wantOut: `Task #2 "Viết API" chuyển sang doing`},
		{name: "done", args: []string{"done", "3"}, wantOut: `Task #3 "Viết tài liệu" chuyển sang done`},
		{name: "assign", args: []string{"assign", "-user", "2", "2"}, wantOut: "Đã giao task #2 cho user #2"},
		{name: "list", args: []string{"list"}, wantOut: "Đã xong 1/3 task, quá hạn 1 task\nCần xử lý gấp: Thiết kế database"},
		{name: "list -status", args: []string{"list", "-status", "doing"}, wantOut: "#2  [p1] Viết API           doing user #2"},
		{name: "list -overdue", args: []string{"list", "-overdue", "-project", "1"}, wantOut: "hạn 10/11/2026 (quá hạn)"},
		{name: "list rỗng", args: []string{"list", "-project", "2"}, wantOut: "Không có task nào"},
		{name: "version", args: []string{"version"}, wantOut: "taskflow dev"},
		{name: "help", args: []string{"help"}, wantOut: "Cách dùng:"},
		{name: "-h của lệnh con", args: []string{"list", "-h"}, wantErr: "Cách dùng: taskflow list"},

		{name: "thiếu lệnh", args: nil, wantCode: 2, wantErr: "Các lệnh:"},
		{name: "lệnh lạ", args: []string{"remove", "1"}, wantCode: 2, wantErr: `không có lệnh "remove"`},
		{name: "flag lạ", args: []string{"list", "-x"}, wantCode: 2, wantErr: "flag provided but not defined: -x"},
		{name: "sai ngày", args: []string{"add", "-project", "1", "-title", "a", "-due", "2026-13-01"}, wantCode: 2,
			wantErr: "cần dạng YYYY-MM-DD"},
		{name: "sai trạng thái", args: []string{"list", "-status", "xong"}, wantCode: 2, wantErr: `trạng thái "xong" không hợp lệ`},
		{name: "id không phải số", args: []string{"start", "abc"}, wantCode: 2, wantErr: `id "abc" không hợp lệ`},
		{name: "flag sau id", args: []string{"assign", "2", "-user", "3"}, wantCode: 2, wantErr: "cần đúng 1 id, nhận 3 tham số"},
		{name: "không có task", args: []string{"done", "9"}, wantCode: 1, wantErr: "taskflow: task #9: không tìm thấy"},
		{name: "title rỗng", args: []string{"add", "-project", "1"}, wantCode: 1, wantErr: "title: không được để trống"},
		{name: "import lỗi", args: []string{"import"}, stdin: "Viết API\n", wantCode: 1, wantErr: "dòng 1: project_id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			a := &app{
				stdin:  strings.NewReader(tt.stdin),
				stdout: &stdout,
				stderr: &stderr,
				now:    func() time.Time { return now },
			}

			code := a.run(append([]string{"-file", file}, tt.args...))

			if code != tt.wantCode {
				t.Errorf("exit code = %d, muốn %d\nstderr: %s", code, tt.wantCode, stderr.String())
			}
			if !strings.Contains(stdout.String(), tt.wantOut) {
				t.Errorf("stdout = %q, muốn chứa %q", stdout.String(), tt.wantOut)
			}
			if !strings.Contains(stderr.String(), tt.wantErr) {
				t.Errorf("stderr = %q, muốn chứa %q", stderr.String(), tt.wantErr)
			}
			if code != 0 && stdout.Len() > 0 {
				t.Errorf("lỗi nhưng vẫn in ra stdout: %q", stdout.String())
			}
		})
	}
}

func TestFileFormat(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tasks.json")
	a := &app{stdin: strings.NewReader(""), stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, now: time.Now}
	for _, args := range [][]string{
		{"add", "-project", "1", "-title", "Viết API"},
		{"start", "1"},
	} {
		if code := a.run(append([]string{"-file", file}, args...)); code != 0 {
			t.Fatalf("%v: exit code %d", args, code)
		}
	}

	// tasks.json dùng key theo tag json, status dạng chữ, bỏ các field zero value
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	want := "[\n  {\n    \"id\": 1,\n    \"project_id\": 1,\n    \"title\": \"Viết API\",\n    \"status\": \"doing\"\n  }\n]"
	if string(data) != want {
		t.Errorf("tasks.json =\n%s\nmuốn\n%s", data, want)
	}
}

func TestDefaultFile(t *testing.T) {
	t.Setenv("TASKFLOW_FILE", "")
	if got := defaultFile(); got != "tasks.json" {
		t.Errorf("defaultFile() = %q, muốn tasks.json", got)
	}
	t.Setenv("TASKFLOW_FILE", "/tmp/work.json")
	if got := defaultFile(); got != "/tmp/work.json" {
		t.Errorf("defaultFile() = %q, muốn /tmp/work.json", got)
	}
}
