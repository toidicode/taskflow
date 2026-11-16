package cache

import (
	"context"
	"testing"
	"time"
)

func TestMemoryTTL(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	m := NewMemory()
	m.now = func() time.Time { return now }

	if _, ok, err := m.Get(ctx, "k"); ok || err != nil {
		t.Fatalf("cache rỗng: ok = %v, err = %v", ok, err)
	}
	m.Set(ctx, "k", []byte("v1"), time.Minute)
	b, ok, _ := m.Get(ctx, "k")
	if !ok || string(b) != "v1" {
		t.Fatalf("ngay sau Set: %q, %v", b, ok)
	}
	b[0] = 'x' // sửa bản nhận được không làm hỏng cache
	if b, _, _ := m.Get(ctx, "k"); string(b) != "v1" {
		t.Errorf("cache bị sửa qua giá trị trả về: %q", b)
	}

	now = now.Add(59 * time.Second)
	if _, ok, _ := m.Get(ctx, "k"); !ok {
		t.Error("chưa hết hạn mà đã mất")
	}
	now = now.Add(time.Second)
	if _, ok, _ := m.Get(ctx, "k"); ok || m.Len() != 0 {
		t.Errorf("hết hạn mà vẫn còn: ok = %v, len = %d", ok, m.Len())
	}

	m.Set(ctx, "a", []byte("1"), time.Minute)
	m.Set(ctx, "b", []byte("2"), time.Minute)
	m.Delete(ctx, "a", "b", "khong-co")
	if m.Len() != 0 {
		t.Errorf("sau Delete còn %d key", m.Len())
	}
}
