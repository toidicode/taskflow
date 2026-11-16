package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestRealIP(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.39.0.10, 10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		remote string
		xff    []string // các header X-Forwarded-For, theo thứ tự
		want   string
	}{
		{"không qua proxy", "203.0.113.7:51234", nil, "203.0.113.7"},
		{"client tự gửi header: bỏ qua", "203.0.113.7:51234", []string{"1.2.3.4"}, "203.0.113.7"},
		{"qua Caddy", "10.39.0.10:40000", []string{"203.0.113.7"}, "203.0.113.7"},
		{"qua Caddy, client gửi IP giả trước", "10.39.0.10:40000", []string{"1.2.3.4, 203.0.113.7"}, "203.0.113.7"},
		{"qua hai proxy tin cậy", "10.39.0.10:40000", []string{"203.0.113.7, 10.1.2.3"}, "203.0.113.7"},
		{"nhiều dòng header", "10.39.0.10:40000", []string{"1.2.3.4", "203.0.113.7"}, "203.0.113.7"},
		{"proxy không gửi header", "10.39.0.10:40000", nil, "10.39.0.10"},
		{"header sai", "10.39.0.10:40000", []string{"không-phải-ip"}, "10.39.0.10"},
		{"IPv6", "[2001:db8::1]:443", nil, "2001:db8::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.remote
			for _, v := range tt.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := realIP(r, trusted); got != tt.want {
				t.Errorf("realIP = %q, muốn %q", got, tt.want)
			}
		})
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
