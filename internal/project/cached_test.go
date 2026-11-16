package project_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/toidicode/taskflow/internal/cache"
	"github.com/toidicode/taskflow/internal/project"
)

// countingStore đếm số lần Get, List thật sự tới store bên trong; delay giả lập truy vấn chậm.
type countingStore struct {
	project.Store
	gets, lists atomic.Int64
	delay       time.Duration
}

func (s *countingStore) Get(ctx context.Context, id int) (project.Project, error) {
	s.gets.Add(1)
	if err := s.wait(ctx); err != nil {
		return project.Project{}, err
	}
	return s.Store.Get(ctx, id)
}

func (s *countingStore) List(ctx context.Context, ownerID int) ([]project.Project, error) {
	s.lists.Add(1)
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	return s.Store.List(ctx, ownerID)
}

// wait chờ delay như một truy vấn thật: context bị hủy thì dừng và trả lỗi.
func (s *countingStore) wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(s.delay):
		return nil
	}
}

func newCached(t *testing.T) (*project.CachedStore, *countingStore) {
	t.Helper()
	inner := &countingStore{Store: project.NewMemoryStore()}
	s := project.NewCachedStore(inner, cache.NewMemory(), project.CacheOptions{})
	for _, name := range []string{"TaskFlow API", "Website"} {
		if err := s.Add(context.Background(), project.New(name, 1)); err != nil {
			t.Fatal(err)
		}
	}
	return s, inner
}

func TestCachedStoreHit(t *testing.T) {
	ctx := context.Background()
	s, inner := newCached(t)
	for range 3 {
		if ps, err := s.List(ctx, 1); err != nil || len(ps) != 2 {
			t.Fatalf("List: %v, %v", ps, err)
		}
		if p, err := s.Get(ctx, 2); err != nil || p.Name != "Website" {
			t.Fatalf("Get: %+v, %v", p, err)
		}
	}
	if inner.lists.Load() != 1 || inner.gets.Load() != 1 {
		t.Errorf("store bị gọi List %d, Get %d lần, muốn 1 và 1", inner.lists.Load(), inner.gets.Load())
	}
	if st := s.Stats(); st != (project.CacheStats{Hits: 4, Misses: 2}) {
		t.Errorf("Stats = %+v", st)
	}

	// không có thì trả lỗi và không cache: lần sau vẫn hỏi store
	for range 2 {
		if _, err := s.Get(ctx, 99); !errors.Is(err, project.ErrNotFound) {
			t.Fatalf("Get(99): %v", err)
		}
	}
	if inner.gets.Load() != 3 {
		t.Errorf("Get(99) hai lần: store bị gọi %d lần tổng cộng, muốn 3", inner.gets.Load())
	}
}

func TestCachedStoreInvalidate(t *testing.T) {
	ctx := context.Background()
	s, _ := newCached(t)
	s.List(ctx, 1) // đưa danh sách và project #1 vào cache
	s.Get(ctx, 1)

	p := project.New("App mobile", 1)
	s.Add(ctx, p)
	if ps, _ := s.List(ctx, 1); len(ps) != 3 {
		t.Errorf("sau Add: %d project, muốn 3", len(ps))
	}

	s.Update(ctx, &project.Project{ID: 1, Name: "TaskFlow API v2", OwnerID: 1})
	if got, _ := s.Get(ctx, 1); got.Name != "TaskFlow API v2" {
		t.Errorf("Get sau Update: %q", got.Name)
	}
	if ps, _ := s.List(ctx, 1); ps[0].Name != "TaskFlow API v2" {
		t.Errorf("List sau Update: %q", ps[0].Name)
	}

	// đổi chủ: danh sách của cả chủ cũ lẫn chủ mới phải được xóa
	s.List(ctx, 2)
	s.Update(ctx, &project.Project{ID: 1, Name: "TaskFlow API v2", OwnerID: 2})
	if ps, _ := s.List(ctx, 1); len(ps) != 2 {
		t.Errorf("chủ cũ còn %d project, muốn 2", len(ps))
	}
	if ps, _ := s.List(ctx, 2); len(ps) != 1 {
		t.Errorf("chủ mới có %d project, muốn 1", len(ps))
	}

	s.Delete(ctx, p.ID)
	if _, err := s.Get(ctx, p.ID); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("Get sau Delete: %v", err)
	}
	if ps, _ := s.List(ctx, 1); len(ps) != 1 {
		t.Errorf("sau Delete: %d project, muốn 1", len(ps))
	}
	if err := s.Delete(ctx, 99); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("Delete(99): %v", err)
	}
}

// TestCachedStoreSingleflight: 50 request cùng miss một key, store chỉ bị đọc một lần.
// synctest: time.Sleep trong store dùng đồng hồ giả, thời gian chỉ trôi khi mọi goroutine đều đang chờ,
// nên chắc chắn cả 50 goroutine đã tới singleflight trước khi lần đọc đầu tiên xong.
func TestCachedStoreSingleflight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inner := &countingStore{Store: project.NewMemoryStore(), delay: 50 * time.Millisecond}
		s := project.NewCachedStore(inner, cache.NewMemory(), project.CacheOptions{})
		s.Add(t.Context(), project.New("TaskFlow API", 1))

		var wg sync.WaitGroup
		for range 50 {
			wg.Go(func() {
				if ps, err := s.List(t.Context(), 1); err != nil || len(ps) != 1 {
					t.Errorf("List: %v, %v", ps, err)
				}
			})
		}
		wg.Wait()
		if n := inner.lists.Load(); n != 1 {
			t.Errorf("store bị đọc %d lần, muốn 1", n)
		}
		if st := s.Stats(); st.Misses != 50 {
			t.Errorf("Stats = %+v, muốn 50 miss", st)
		}
	})
}

// TestCachedStoreFirstCallerCanceled: request đầu tiên bị hủy giữa chừng không làm hỏng các request đang chờ.
func TestCachedStoreFirstCallerCanceled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inner := &countingStore{Store: project.NewMemoryStore(), delay: 50 * time.Millisecond}
		s := project.NewCachedStore(inner, cache.NewMemory(), project.CacheOptions{})
		s.Add(t.Context(), project.New("TaskFlow API", 1))

		ctx, cancel := context.WithCancel(t.Context())
		var wg sync.WaitGroup
		wg.Go(func() { s.List(ctx, 1) })
		synctest.Wait() // request đầu đã vào store
		wg.Go(func() {
			if _, err := s.List(t.Context(), 1); err != nil {
				t.Errorf("request chờ nhận lỗi: %v", err)
			}
		})
		synctest.Wait()
		cancel()
		wg.Wait()
	})
}

// brokenCache giả lập Redis chết: mọi lệnh đều lỗi.
type brokenCache struct{}

var errDown = errors.New("dial tcp: connection refused")

func (brokenCache) Get(context.Context, string) ([]byte, bool, error)        { return nil, false, errDown }
func (brokenCache) Set(context.Context, string, []byte, time.Duration) error { return errDown }
func (brokenCache) Delete(context.Context, ...string) error                  { return errDown }

func TestCachedStoreCacheDown(t *testing.T) {
	ctx := context.Background()
	s := project.NewCachedStore(project.NewMemoryStore(), brokenCache{}, project.CacheOptions{})
	p := project.New("TaskFlow API", 1)
	if err := s.Add(ctx, p); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if ps, err := s.List(ctx, 1); err != nil || len(ps) != 1 {
		t.Fatalf("List: %v, %v", ps, err)
	}
	if got, err := s.Get(ctx, p.ID); err != nil || got.Name != "TaskFlow API" {
		t.Fatalf("Get: %+v, %v", got, err)
	}
	if st := s.Stats(); st != (project.CacheStats{Misses: 2, Errors: 3}) { // 1 xóa + 2 đọc, đọc lỗi thì không ghi
		t.Errorf("Stats = %+v", st)
	}
}

// ttlCache ghi lại TTL của mỗi lần Set.
type ttlCache struct {
	*cache.Memory
	mu   sync.Mutex
	ttls []time.Duration
}

func (c *ttlCache) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	c.mu.Lock()
	c.ttls = append(c.ttls, ttl)
	c.mu.Unlock()
	return c.Memory.Set(ctx, key, val, ttl)
}

func TestCachedStoreTTLJitter(t *testing.T) {
	ctx := context.Background()
	c := &ttlCache{Memory: cache.NewMemory()}
	s := project.NewCachedStore(project.NewMemoryStore(), c, project.CacheOptions{TTL: time.Minute})
	for id := 1; id <= 100; id++ {
		s.List(ctx, id)
	}
	distinct := map[time.Duration]bool{}
	for _, ttl := range c.ttls {
		if ttl < time.Minute || ttl > 66*time.Second {
			t.Fatalf("TTL %v ngoài khoảng [1m, 1m6s]", ttl)
		}
		distinct[ttl] = true
	}
	if len(distinct) < 50 {
		t.Errorf("100 key chỉ có %d TTL khác nhau: thiếu jitter", len(distinct))
	}
}
