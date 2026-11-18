// Package metrics đếm và đo TaskFlow API bằng Prometheus: request HTTP (RED), job, cache,
// hàng đợi, rồi phục vụ chúng ở /metrics cho Prometheus đến lấy (pull).
package metrics

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/toidicode/taskflow/internal/jobs"
)

// Metrics giữ registry riêng của một bản API (không dùng prometheus.DefaultRegisterer: test tạo
// bao nhiêu bản cũng không đụng nhau) và các metric của nó.
type Metrics struct {
	reg         *prometheus.Registry
	requests    *prometheus.CounterVec   // method, route, status
	duration    *prometheus.HistogramVec // method, route
	inFlight    prometheus.Gauge
	jobs        *prometheus.CounterVec   // type, result
	jobDuration *prometheus.HistogramVec // type
}

// New tạo Metrics, kèm metric của Go runtime (goroutine, GC, bộ nhớ) và của process (CPU, file mở).
func New() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	f := promauto.With(reg) // promauto: tạo metric và đăng ký luôn vào reg
	return &Metrics{
		reg: reg,
		requests: f.NewCounterVec(prometheus.CounterOpts{
			Name: "taskflow_http_requests_total",
			Help: "Số request HTTP đã xử lý.",
		}, []string{"method", "route", "status"}),
		duration: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "taskflow_http_request_duration_seconds",
			Help:    "Thời gian xử lý request HTTP.",
			Buckets: prometheus.DefBuckets, // 5ms tới 10s
		}, []string{"method", "route"}),
		inFlight: f.NewGauge(prometheus.GaugeOpts{
			Name: "taskflow_http_requests_in_flight",
			Help: "Số request HTTP đang xử lý.",
		}),
		jobs: f.NewCounterVec(prometheus.CounterOpts{
			Name: "taskflow_jobs_total",
			Help: "Số lần chạy job, result là ok hoặc error.",
		}, []string{"type", "result"}),
		jobDuration: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "taskflow_job_duration_seconds",
			Help:    "Thời gian một lần chạy job.",
			Buckets: []float64{.01, .05, .1, .5, 1, 5, 10, 30},
		}, []string{"type"}),
	}
}

// Handler phục vụ mọi metric theo định dạng text của Prometheus.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// knownMethods: method lạ (client gửi gì cũng được) gom thành OTHER, để không ai tạo được
// vô số chuỗi thời gian bằng cách gửi method tùy ý.
var knownMethods = map[string]bool{"GET": true, "HEAD": true, "POST": true, "PUT": true,
	"PATCH": true, "DELETE": true, "OPTIONS": true}

// Gin là middleware đo request: tổng số request theo status (Rate, Errors) và thời gian (Duration).
func (m *Metrics) Gin(c *gin.Context) {
	m.inFlight.Inc()
	defer m.inFlight.Dec()
	start := time.Now()
	c.Next()

	// route pattern, không phải URL thật: /api/v1/tasks/:id chứ không phải /api/v1/tasks/42
	route := c.FullPath()
	if route == "" {
		route = "unmatched" // 404: path do client tự đặt, không đưa vào label
	}
	method := c.Request.Method
	if !knownMethods[method] {
		method = "OTHER"
	}
	status := strconv.Itoa(c.Writer.Status())
	m.requests.WithLabelValues(method, route, status).Inc()
	m.duration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
}

// Job bọc h để đếm số lần chạy và đo thời gian. m nil thì trả lại h.
func (m *Metrics) Job(h jobs.Handler) jobs.Handler {
	if m == nil {
		return h
	}
	run := h.Run
	h.Run = func(ctx context.Context, payload []byte) error {
		start := time.Now()
		err := run(ctx, payload)
		result := "ok"
		if err != nil {
			result = "error"
		}
		m.jobs.WithLabelValues(h.Type, result).Inc()
		m.jobDuration.WithLabelValues(h.Type).Observe(time.Since(start).Seconds())
		return err
	}
	return h
}

// Cache đăng ký số lần hit, miss, lỗi của cache name; stats được gọi mỗi lần Prometheus đến lấy,
// đọc thẳng bộ đếm atomic của cache (Bài 43), không phải đếm hai lần.
func (m *Metrics) Cache(name string, stats func() (hits, misses, errors int64)) {
	for i, result := range []string{"hit", "miss", "error"} {
		m.reg.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name:        "taskflow_cache_requests_total",
			Help:        "Số lần đọc cache theo kết quả.",
			ConstLabels: prometheus.Labels{"cache": name, "result": result},
		}, func() float64 {
			h, mi, e := stats()
			return float64([]int64{h, mi, e}[i])
		}))
	}
}

// Queue đăng ký số job theo queue và trạng thái, đọc bằng stats (jobs.Redis.Stats) lúc Prometheus đến lấy.
func (m *Metrics) Queue(stats func() ([]jobs.QueueStats, error)) {
	m.reg.MustRegister(queueCollector{stats})
}

var queueDesc = prometheus.NewDesc("taskflow_queue_tasks",
	"Số job trong hàng đợi theo queue và trạng thái.", []string{"queue", "state"}, nil)

// queueCollector là một prometheus.Collector: tự tạo giá trị mỗi lần được hỏi thay vì giữ bộ đếm.
type queueCollector struct {
	stats func() ([]jobs.QueueStats, error)
}

func (q queueCollector) Describe(ch chan<- *prometheus.Desc) { ch <- queueDesc }

func (q queueCollector) Collect(ch chan<- prometheus.Metric) {
	all, err := q.stats()
	if err != nil {
		// Redis lỗi: lần lấy này thiếu metric hàng đợi, Prometheus vẫn nhận các metric khác
		slog.Warn("không đọc được số job trong hàng đợi", "err", err)
		return
	}
	for _, s := range all {
		for state, n := range map[string]int{"pending": s.Pending, "active": s.Active, "scheduled": s.Scheduled,
			"retry": s.Retry, "archived": s.Archived, "completed": s.Completed} {
			ch <- prometheus.MustNewConstMetric(queueDesc, prometheus.GaugeValue, float64(n), s.Queue, state)
		}
	}
}
