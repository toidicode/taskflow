package realtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/testredis"
)

// recv lấy một message đang chờ của s, không có thì trả "".
func recv(s *Subscription) string {
	select {
	case m := <-s.Messages():
		return string(m)
	default:
		return ""
	}
}

func TestHubBroadcast(t *testing.T) {
	h := NewHub(Options{})
	a, _ := h.Subscribe(1, 1)
	b, _ := h.Subscribe(1, 1)
	other, _ := h.Subscribe(2, 2)

	h.Broadcast(1, []byte("m1"))
	if recv(a) != "m1" || recv(b) != "m1" || recv(other) != "" {
		t.Error("message của project #1 phải tới đúng hai kết nối của project #1")
	}
	b.Close()
	b.Close() // gọi lại không sao
	h.Broadcast(1, []byte("m2"))
	if recv(a) != "m2" || recv(b) != "" || h.Len() != 2 {
		t.Errorf("sau khi b Close: Len = %d", h.Len())
	}
}

func TestHubSlowConsumer(t *testing.T) {
	h := NewHub(Options{Buffer: 2})
	slow, _ := h.Subscribe(1, 1)
	fast, _ := h.Subscribe(1, 2)
	for _, m := range []string{"m1", "m2", "m3"} {
		h.Broadcast(1, []byte(m))
		recv(fast) // fast đọc kịp
	}
	select {
	case <-slow.Done():
	default:
		t.Fatal("hàng chờ đầy mà kết nối chậm chưa bị ngắt")
	}
	if !errors.Is(slow.Err(), ErrSlowConsumer) || fast.Err() != nil || h.Len() != 1 {
		t.Errorf("slow.Err = %v, fast.Err = %v, Len = %d", slow.Err(), fast.Err(), h.Len())
	}
}

func TestHubMaxPerUser(t *testing.T) {
	h := NewHub(Options{MaxPerUser: 2})
	s1, _ := h.Subscribe(1, 1)
	h.Subscribe(2, 1)
	if _, err := h.Subscribe(1, 1); !errors.Is(err, ErrTooManyConnections) {
		t.Errorf("kết nối thứ ba: %v, muốn ErrTooManyConnections", err)
	}
	if _, err := h.Subscribe(1, 2); err != nil {
		t.Errorf("user khác: %v", err)
	}
	s1.Close()
	if _, err := h.Subscribe(1, 1); err != nil {
		t.Errorf("sau khi đóng một kết nối: %v", err)
	}
}

func TestHubClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHub(Options{})
		var wg sync.WaitGroup
		for i := range 3 {
			s, _ := h.Subscribe(1, i+1)
			wg.Go(func() { // như handler: chờ Done, "gửi close frame" rồi Close
				<-s.Done()
				time.Sleep(time.Second)
				s.Close()
			})
		}
		h.Close()
		if _, err := h.Subscribe(1, 9); !errors.Is(err, ErrClosed) {
			t.Errorf("Subscribe sau Close: %v", err)
		}
		start := time.Now()
		if err := h.Wait(context.Background()); err != nil || time.Since(start) != time.Second {
			t.Errorf("Wait: %v sau %v, muốn chờ đủ 1s", err, time.Since(start))
		}
		wg.Wait()
	})
}

func TestMemoryTickets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		m := NewMemoryTickets()
		testTickets(t, m)

		old, _ := m.Issue(ctx, 1)
		time.Sleep(TicketTTL + time.Second)
		if _, err := m.Redeem(ctx, old); !errors.Is(err, ErrInvalidTicket) {
			t.Errorf("vé hết hạn: %v", err)
		}
		m.Issue(ctx, 1) // dọn vé hết hạn
		if len(m.tickets) != 1 {
			t.Errorf("còn %d vé, muốn 1", len(m.tickets))
		}
	})
}

// testTickets kiểm tra phần chung của mọi Tickets: vé đổi được đúng một lần, vé lạ bị từ chối.
func testTickets(t *testing.T, tk Tickets) {
	t.Helper()
	ctx := context.Background()
	v, err := tk.Issue(ctx, 7)
	if err != nil || len(v) != 26 {
		t.Fatalf("Issue: %q, %v", v, err)
	}
	if id, err := tk.Redeem(ctx, v); id != 7 || err != nil {
		t.Errorf("Redeem lần đầu: %d, %v; muốn 7", id, err)
	}
	if _, err := tk.Redeem(ctx, v); !errors.Is(err, ErrInvalidTicket) {
		t.Errorf("Redeem lần hai: %v, muốn ErrInvalidTicket", err)
	}
	if _, err := tk.Redeem(ctx, "ve-tu-che"); !errors.Is(err, ErrInvalidTicket) {
		t.Errorf("vé lạ: %v", err)
	}
}

func TestRedisTickets(t *testing.T) {
	rc, prefix := testredis.New(t)
	tk := NewRedisTickets(rc.Client(), prefix+"ticket:")
	testTickets(t, tk)
	v, _ := tk.Issue(context.Background(), 1)
	if ttl := rc.Client().PTTL(context.Background(), prefix+"ticket:"+v).Val(); ttl <= 0 || ttl > TicketTTL {
		t.Errorf("PTTL = %v, muốn tối đa %v", ttl, TicketTTL)
	}
}

// TestRedisFanOut: hai bản API (hai Hub) dùng chung Redis; event gửi từ bản A tới được client của bản B.
func TestRedisFanOut(t *testing.T) {
	rc, prefix := testredis.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hubA, hubB := NewHub(Options{}), NewHub(Options{})
	a := NewRedis(rc.Client(), prefix+"events", hubA)
	b := NewRedis(rc.Client(), prefix+"events", hubB)
	go a.Run(ctx)
	go b.Run(ctx)
	testredis.WaitSubscribers(t, rc, prefix+"events", 2)

	onB, _ := hubB.Subscribe(1, 1)
	otherProject, _ := hubB.Subscribe(2, 1)
	if err := a.Publish(ctx, task.Event{Type: task.EventDeleted, ProjectID: 1, TaskID: 5, By: 1}); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-onB.Messages():
		if want := `{"type":"task.deleted","project_id":1,"task_id":5,"by":1}`; string(m) != want {
			t.Errorf("message = %s, muốn %s", m, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client ở bản B không nhận được event gửi từ bản A")
	}
	if recv(otherProject) != "" {
		t.Error("project #2 nhận event của project #1")
	}
}
