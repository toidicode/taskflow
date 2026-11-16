package httpapi

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestClientIP: c.ClientIP() của engine do newEngine tạo cho đúng kết quả như realIP tự viết ở Bài 39.
func TestClientIP(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.39.0.10, 10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	var got string
	r := newEngine(trusted)
	r.GET("/", func(c *gin.Context) { got = c.ClientIP() })

	tests := []struct {
		name   string
		remote string
		header string   // tên header, mặc định X-Forwarded-For
		values []string // các dòng của header, theo thứ tự
		want   string
	}{
		{"không qua proxy", "203.0.113.7:51234", "", nil, "203.0.113.7"},
		{"client tự gửi header: bỏ qua", "203.0.113.7:51234", "", []string{"1.2.3.4"}, "203.0.113.7"},
		{"qua Caddy", "10.39.0.10:40000", "", []string{"203.0.113.7"}, "203.0.113.7"},
		{"qua Caddy, client gửi IP giả trước", "10.39.0.10:40000", "", []string{"1.2.3.4, 203.0.113.7"}, "203.0.113.7"},
		{"qua hai proxy tin cậy", "10.39.0.10:40000", "", []string{"203.0.113.7, 10.1.2.3"}, "203.0.113.7"},
		{"nhiều dòng header", "10.39.0.10:40000", "", []string{"1.2.3.4", "203.0.113.7"}, "203.0.113.7"},
		{"proxy không gửi header", "10.39.0.10:40000", "", nil, "10.39.0.10"},
		{"header sai", "10.39.0.10:40000", "", []string{"không-phải-ip"}, "10.39.0.10"},
		{"IPv6", "[2001:db8::1]:443", "", nil, "2001:db8::1"},
		{"X-Real-IP không được đọc", "10.39.0.10:40000", "X-Real-IP", []string{"1.2.3.4"}, "10.39.0.10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remote
			header := tt.header
			if header == "" {
				header = "X-Forwarded-For"
			}
			for _, v := range tt.values {
				req.Header.Add(header, v)
			}
			got = ""
			r.ServeHTTP(httptest.NewRecorder(), req)
			if got != tt.want {
				t.Errorf("ClientIP = %q, muốn %q", got, tt.want)
			}
		})
	}
}

// TestClientIPDefault: engine Gin mặc định tin mọi proxy, IP client tự khai cũng được nhận.
func TestClientIPDefault(t *testing.T) {
	var got string
	r := gin.New()
	r.GET("/", func(c *gin.Context) { got = c.ClientIP() })
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.7:51234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.ServeHTTP(httptest.NewRecorder(), req)
	if got != "1.2.3.4" {
		t.Errorf("ClientIP = %q, muốn 1.2.3.4 (IP giả)", got)
	}
}

func TestParseTrustedProxies(t *testing.T) {
	got, err := ParseTrustedProxies(" 10.39.0.10 ,10.39.0.7/24,, ::1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.39.0.10/32", "10.39.0.0/24", "::1/128"}
	if len(got) != len(want) {
		t.Fatalf("got %v, muốn %v", got, want)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Errorf("[%d] = %s, muốn %s", i, got[i], want[i])
		}
	}
	if _, err := ParseTrustedProxies("caddy"); err == nil {
		t.Error("tên host phải là lỗi")
	}
}

func TestNewAPIClientIP(t *testing.T) {
	logs := captureLog(t)
	trusted, _ := ParseTrustedProxies("10.39.0.10")
	api := newAPI(newTestStores(t), testTokens, "", trusted)

	req := httptest.NewRequest("GET", "/health", nil)
	req.RemoteAddr = "10.39.0.10:40000"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	api.ServeHTTP(httptest.NewRecorder(), req)

	entries := logEntries(t, logs)
	if len(entries) != 1 || entries[0]["client_ip"] != "203.0.113.7" {
		t.Errorf("log = %v, muốn client_ip 203.0.113.7", entries)
	}
}
