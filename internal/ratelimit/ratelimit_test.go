package ratelimit

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"
	"time"
)

func TestParseLimit(t *testing.T) {
	tests := []struct {
		in      string
		want    Limit
		str     string
		wantErr bool
	}{
		{"10/1m", Limit{10, time.Minute}, "10/1m", false},
		{"5/10m", Limit{5, 10 * time.Minute}, "5/10m", false},
		{"600/1m0s", Limit{600, time.Minute}, "600/1m", false},
		{"100/1h", Limit{100, time.Hour}, "100/1h", false},
		{"3/1h30m", Limit{3, 90 * time.Minute}, "3/1h30m", false},
		{"2/30s", Limit{2, 30 * time.Second}, "2/30s", false},
		{"off", Limit{}, "off", false},
		{"10", Limit{}, "", true},
		{"a/1m", Limit{}, "", true},
		{"0/1m", Limit{}, "", true},
		{"-1/1m", Limit{}, "", true},
		{"10/0s", Limit{}, "", true},
		{"10/phút", Limit{}, "", true},
		{"1000/1s", Limit{1000, time.Second}, "1000/1s", false},
		{"2000/1s", Limit{}, "", true},    // 0,5ms một token: dưới 1ms
		{"2000000/1s", Limit{}, "", true}, // interval 500ns, sang µs thành 0
	}
	for _, tt := range tests {
		got, err := ParseLimit(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("ParseLimit(%q) = %v, %v; muốn %v, lỗi %v", tt.in, got, err, tt.want, tt.wantErr)
			continue
		}
		if err == nil && got.String() != tt.str {
			t.Errorf("String() = %q, muốn %q", got.String(), tt.str)
		}
	}
}

// limiterSteps chạy cùng kịch bản cho mọi Limiter: 3 request mỗi phút (20 giây một token).
// sleep là hàm chờ: time.Sleep thật hoặc trong bong bóng synctest.
func limiterSteps(t *testing.T, l Limiter, key string, exact bool) {
	t.Helper()
	ctx := context.Background()
	lim := Limit{N: 3, Period: time.Minute}
	for i, want := range []int{2, 1, 0} {
		res, err := l.Allow(ctx, key, lim)
		if err != nil || !res.Allowed || res.Remaining != want {
			t.Fatalf("request %d: %+v, %v; muốn được đi, còn %d", i+1, res, err, want)
		}
		// token kế tiếp có sau tối đa 20 giây: trong 20 giây đó chỉ đi được Remaining request
		if res.Window <= 19*time.Second || res.Window > 20*time.Second {
			t.Errorf("request %d: Window %v, muốn gần 20s", i+1, res.Window)
		}
	}
	res, err := l.Allow(ctx, key, lim)
	if err != nil || res.Allowed {
		t.Fatalf("request 4: %+v, %v; muốn bị chặn", res, err)
	}
	// Redis tính thời gian thật nên chỉ so gần đúng
	if exact && (res.RetryAfter != 20*time.Second || res.Window != 20*time.Second) {
		t.Errorf("request 4: RetryAfter %v, Window %v; muốn 20s, 20s", res.RetryAfter, res.Window)
	}
	if res.Window != res.RetryAfter {
		t.Errorf("request 4: Window %v khác RetryAfter %v", res.Window, res.RetryAfter)
	}
	if res.RetryAfter <= 19*time.Second || res.RetryAfter > 20*time.Second {
		t.Errorf("request 4: RetryAfter %v, muốn gần 20s", res.RetryAfter)
	}
	if res, _ := l.Allow(ctx, key+"-khac", lim); !res.Allowed {
		t.Errorf("key khác phải có bucket riêng")
	}
}

// TestMemory chạy trong bong bóng synctest: time.Now() là đồng hồ giả, time.Sleep trôi ngay lập tức.
func TestMemory(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := NewMemory()
		limiterSteps(t, m, "ip:203.0.113.7", true)

		ctx, lim := context.Background(), Limit{N: 3, Period: time.Minute}
		time.Sleep(10 * time.Second)
		if res, _ := m.Allow(ctx, "ip:203.0.113.7", lim); res.Allowed || res.RetryAfter != 10*time.Second || res.Window != 10*time.Second {
			t.Errorf("sau 10s: %+v, muốn còn chờ 10s", res)
		}
		time.Sleep(10 * time.Second) // đủ 20s: có đúng một token
		if res, _ := m.Allow(ctx, "ip:203.0.113.7", lim); !res.Allowed || res.Remaining != 0 || res.Window != 20*time.Second {
			t.Errorf("sau 20s: %+v, muốn được đi, còn 0, token kế tiếp sau 20s", res)
		}
	})
}

// TestMemorySweep: bucket đã đầy lại bị xóa ở lần dọn kế tiếp, số key không tăng mãi.
func TestMemorySweep(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := NewMemory()
		ctx, lim := context.Background(), Limit{N: 3, Period: time.Minute}
		for i := range 1000 {
			m.Allow(ctx, fmt.Sprintf("ip:10.0.%d.%d", i/256, i%256), lim)
		}
		if n := m.Len(); n != 1000 {
			t.Fatalf("Len = %d, muốn 1000", n)
		}
		time.Sleep(30 * time.Second) // chưa tới lúc dọn
		for range 3 {
			m.Allow(ctx, "ip:203.0.113.7", lim) // dùng hết 3 token: 1 phút nữa mới đầy lại
		}
		if n := m.Len(); n != 1001 {
			t.Fatalf("sau 30s: Len = %d, muốn 1001", n)
		}
		time.Sleep(31 * time.Second) // đã quá 1 phút: lần Allow này dọn, 1000 bucket cũ đã đầy lại
		m.Allow(ctx, "ip:198.51.100.1", lim)
		if n := m.Len(); n != 2 { // 203.0.113.7 (chưa đầy) và key mới
			t.Errorf("sau khi dọn: Len = %d, muốn 2", n)
		}
	})
}

// TestMemoryConcurrent: 50 goroutine cùng lấy token của một key 10 token, đúng 10 request được đi.
func TestMemoryConcurrent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := NewMemory()
		results := make(chan bool, 50)
		for range 50 {
			go func() {
				res, _ := m.Allow(context.Background(), "user:1", Limit{N: 10, Period: time.Minute})
				results <- res.Allowed
			}()
		}
		synctest.Wait()
		close(results)
		n := 0
		for ok := range results {
			if ok {
				n++
			}
		}
		if n != 10 {
			t.Errorf("%d request được đi, muốn 10", n)
		}
	})
}

func TestDisabled(t *testing.T) {
	if _, err := NewMemory().Allow(context.Background(), "k", Limit{}); err == nil {
		t.Error("Limit{} phải trả lỗi: nơi gọi không được hỏi limiter khi giới hạn tắt")
	}
}
