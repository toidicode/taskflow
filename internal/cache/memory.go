package cache

import (
	"context"
	"sync"
	"time"
)

// Memory là cache trong bộ nhớ của một process, cùng cách dùng với Redis.
// Dùng cho test và chạy thử; nhiều bản API chạy song song thì mỗi bản có cache riêng,
// xóa cache ở bản này không xóa được ở bản kia, nên production dùng Redis.
type Memory struct {
	mu    sync.Mutex
	items map[string]item
	now   func() time.Time // test thay được đồng hồ
}

type item struct {
	val     []byte
	expires time.Time
}

// NewMemory tạo một cache rỗng.
func NewMemory() *Memory {
	return &Memory{items: make(map[string]item), now: time.Now}
}

func (m *Memory) Get(ctx context.Context, key string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[key]
	if !ok || !m.now().Before(it.expires) {
		delete(m.items, key)
		return nil, false, nil
	}
	return append([]byte(nil), it.val...), true, nil // bản sao: nơi gọi sửa không ảnh hưởng cache
}

func (m *Memory) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[key] = item{val: append([]byte(nil), val...), expires: m.now().Add(ttl)}
	return nil
}

func (m *Memory) Delete(ctx context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.items, k)
	}
	return nil
}

// Len trả về số key đang có (kể cả key đã hết hạn mà chưa bị đọc), dùng trong test.
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.items)
}
