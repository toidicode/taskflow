package notify

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestLog(t *testing.T) {
	var buf bytes.Buffer
	n := Log{Logger: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{} // bỏ thời gian để so sánh được
			}
			return a
		},
	}))}
	err := n.TaskDone(context.Background(), TaskDone{TaskID: 3, Title: "Viết test", DoneBy: 2, To: []int{1, 5}})
	if err != nil {
		t.Fatal(err)
	}
	want := `level=INFO msg="thông báo: task đã xong" to_user=1 task_id=3 title="Viết test" done_by=2
level=INFO msg="thông báo: task đã xong" to_user=5 task_id=3 title="Viết test" done_by=2
`
	if got := buf.String(); got != want {
		t.Errorf("log =\n%s\nmuốn\n%s", got, want)
	}
	if strings.Count(buf.String(), "\n") != 2 {
		t.Error("mỗi người nhận phải là một dòng")
	}
}
