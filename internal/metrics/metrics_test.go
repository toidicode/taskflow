package metrics

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/toidicode/taskflow/internal/jobs"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

// TestGinLabels: label route là pattern của Gin, không phải URL thật; path lạ và method lạ bị gom lại.
func TestGinLabels(t *testing.T) {
	m := New()
	r := gin.New()
	r.Use(m.Gin)
	r.GET("/api/v1/tasks/:id", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	for _, req := range []struct{ method, path string }{
		{"GET", "/api/v1/tasks/1"}, {"GET", "/api/v1/tasks/2"}, {"GET", "/api/v1/tasks/3"},
		{"GET", "/wp-login.php"}, {"BREW", "/api/v1/tasks/1"},
	} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(req.method, req.path, nil))
	}

	if got := testutil.ToFloat64(m.requests.WithLabelValues("GET", "/api/v1/tasks/:id", "200")); got != 3 {
		t.Errorf("GET /api/v1/tasks/:id 200 = %v, muốn 3", got)
	}
	if got := testutil.ToFloat64(m.requests.WithLabelValues("GET", "unmatched", "404")); got != 1 {
		t.Errorf("GET unmatched 404 = %v, muốn 1", got)
	}
	if got := testutil.ToFloat64(m.requests.WithLabelValues("OTHER", "unmatched", "404")); got != 1 {
		t.Errorf("OTHER unmatched 404 = %v, muốn 1", got)
	}
	// 3 chuỗi thời gian, dù có 5 URL khác nhau
	if n := testutil.CollectAndCount(m.requests); n != 3 {
		t.Errorf("taskflow_http_requests_total có %d chuỗi, muốn 3", n)
	}
	if got := testutil.ToFloat64(m.inFlight); got != 0 {
		t.Errorf("in flight = %v sau khi mọi request xong, muốn 0", got)
	}
}

func TestJobCacheQueue(t *testing.T) {
	m := New()
	h := m.Job(jobs.Handler{Type: "notify:task_done:v1", Run: func(_ context.Context, p []byte) error {
		if string(p) == "lỗi" {
			return errors.New("lỗi")
		}
		return nil
	}})
	h.Run(context.Background(), nil)
	h.Run(context.Background(), []byte("lỗi"))
	m.Cache("project", func() (int64, int64, int64) { return 7, 2, 1 })
	m.Queue(func() ([]jobs.QueueStats, error) {
		return []jobs.QueueStats{{Queue: "default", Pending: 3, Archived: 1}}, nil
	})

	want := `
# HELP taskflow_cache_requests_total Số lần đọc cache theo kết quả.
# TYPE taskflow_cache_requests_total counter
taskflow_cache_requests_total{cache="project",result="error"} 1
taskflow_cache_requests_total{cache="project",result="hit"} 7
taskflow_cache_requests_total{cache="project",result="miss"} 2
# HELP taskflow_jobs_total Số lần chạy job, result là ok hoặc error.
# TYPE taskflow_jobs_total counter
taskflow_jobs_total{result="error",type="notify:task_done:v1"} 1
taskflow_jobs_total{result="ok",type="notify:task_done:v1"} 1
`
	err := testutil.GatherAndCompare(m.reg, strings.NewReader(want), "taskflow_cache_requests_total", "taskflow_jobs_total")
	if err != nil {
		t.Error(err)
	}

	// qua HTTP như Prometheus
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, line := range []string{
		`taskflow_queue_tasks{queue="default",state="archived"} 1`,
		`taskflow_queue_tasks{queue="default",state="pending"} 3`,
		`taskflow_job_duration_seconds_count{type="notify:task_done:v1"} 2`,
		"go_goroutines ",
	} {
		if !strings.Contains(body, line) {
			t.Errorf("/metrics thiếu %q", line)
		}
	}
}

func TestJobNil(t *testing.T) {
	var m *Metrics // metrics tắt
	h := jobs.Handler{Type: "x", Run: func(context.Context, []byte) error { return nil }}
	if got := m.Job(h); got.Run == nil {
		t.Error("Job(nil) phải trả lại handler")
	}
}
