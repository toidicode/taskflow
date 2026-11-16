package project

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/toidicode/taskflow/internal/reqlog"
)

// Cache là nơi lưu tạm dạng key → []byte có thời hạn mà CachedStore cần.
// Khai báo ở nơi dùng như Tasks; cache.Redis và cache.Memory cài nó.
type Cache interface {
	// Get trả về giá trị của key, ok là false khi không có (miss). err chỉ dành cho lỗi thật.
	Get(ctx context.Context, key string) (val []byte, ok bool, err error)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	Delete(ctx context.Context, keys ...string) error
}

// CacheOptions cấu hình CachedStore, field để trống dùng giá trị mặc định.
type CacheOptions struct {
	TTL    time.Duration // thời gian sống của một key, mặc định 1 phút, cộng thêm tới 10% ngẫu nhiên
	Prefix string        // tiền tố của mọi key, mặc định "taskflow:"
}

// CacheStats đếm số lần đọc trúng cache, trượt cache và lỗi khi gọi cache.
type CacheStats struct {
	Hits, Misses, Errors int64
}

// CachedStore bọc một Store và cache Get, List theo kiểu cache-aside: đọc cache trước,
// miss thì đọc store rồi ghi vào cache; Add, Update, Delete ghi store xong thì xóa key liên quan.
// Cache lỗi (Redis chết, timeout) chỉ ghi log, mọi thao tác vẫn chạy thẳng trên store.
type CachedStore struct {
	next   Store
	cache  Cache
	ttl    time.Duration
	prefix string
	group  singleflight.Group // gom các lần đọc store của cùng một key

	hits, misses, fails atomic.Int64
}

// Kiểm tra lúc biên dịch: *CachedStore phải thỏa mãn Store, nơi dùng Store không cần biết có cache.
var _ Store = (*CachedStore)(nil)

// NewCachedStore bọc next bằng cache c.
func NewCachedStore(next Store, c Cache, opts CacheOptions) *CachedStore {
	if opts.TTL <= 0 {
		opts.TTL = time.Minute
	}
	if opts.Prefix == "" {
		opts.Prefix = "taskflow:"
	}
	return &CachedStore{next: next, cache: c, ttl: opts.TTL, prefix: opts.Prefix}
}

// Key gồm tiền tố, loại dữ liệu, phiên bản định dạng (v1) và ID. Thêm hay đổi field của
// Project thì tăng v1 lên v2: bản mới không đọc nhầm JSON kiểu cũ, key cũ tự hết hạn.
func (s *CachedStore) projectKey(id int) string {
	return s.prefix + "project:v1:" + strconv.Itoa(id)
}

func (s *CachedStore) listKey(ownerID int) string {
	return s.prefix + "projects:v1:owner:" + strconv.Itoa(ownerID)
}

func (s *CachedStore) Get(ctx context.Context, id int) (Project, error) {
	return cached(ctx, s, s.projectKey(id), func(ctx context.Context) (Project, error) {
		return s.next.Get(ctx, id)
	})
}

func (s *CachedStore) List(ctx context.Context, ownerID int) ([]Project, error) {
	ps, err := cached(ctx, s, s.listKey(ownerID), func(ctx context.Context) ([]Project, error) {
		return s.next.List(ctx, ownerID)
	})
	return slices.Clone(ps), err // singleflight trả cùng một slice cho mọi request đang chờ: mỗi request một bản
}

func (s *CachedStore) Add(ctx context.Context, p *Project) error {
	if err := s.next.Add(ctx, p); err != nil {
		return err
	}
	s.invalidate(ctx, s.listKey(p.OwnerID))
	return nil
}

func (s *CachedStore) Update(ctx context.Context, p *Project) error {
	old, err := s.next.Get(ctx, p.ID) // đọc thẳng store: cần chủ cũ để xóa danh sách của người đó
	if err != nil {
		return err
	}
	if err := s.next.Update(ctx, p); err != nil {
		return err
	}
	keys := []string{s.projectKey(p.ID), s.listKey(old.OwnerID)}
	if p.OwnerID != old.OwnerID {
		keys = append(keys, s.listKey(p.OwnerID))
	}
	s.invalidate(ctx, keys...)
	return nil
}

func (s *CachedStore) Delete(ctx context.Context, id int) error {
	old, err := s.next.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.next.Delete(ctx, id); err != nil {
		return err
	}
	s.invalidate(ctx, s.projectKey(id), s.listKey(old.OwnerID))
	return nil
}

// Stats trả về số lần hit, miss và lỗi tính từ lúc tạo store.
func (s *CachedStore) Stats() CacheStats {
	return CacheStats{Hits: s.hits.Load(), Misses: s.misses.Load(), Errors: s.fails.Load()}
}

// cached đọc key trong cache; miss thì gọi load một lần cho mọi request cùng miss key đó
// rồi ghi kết quả vào cache. Lỗi của cache không bao giờ thành lỗi của request.
func cached[T any](ctx context.Context, s *CachedStore, key string, load func(context.Context) (T, error)) (T, error) {
	log := reqlog.Logger(ctx)
	b, ok, err := s.cache.Get(ctx, key)
	cacheDown := err != nil
	if cacheDown {
		s.fails.Add(1)
		log.Warn("cache: đọc lỗi, đọc thẳng database", "key", key, "err", err)
	} else if ok {
		var v T
		if err := json.Unmarshal(b, &v); err == nil {
			s.hits.Add(1)
			log.Debug("cache hit", "key", key)
			return v, nil
		}
		// JSON hỏng hoặc khác kiểu: coi như miss, lần ghi dưới đây đè lên
	}
	s.misses.Add(1)

	// singleflight: 100 request cùng miss một key thì chỉ một request đọc database,
	// 99 request còn lại chờ và nhận chung kết quả (shared = true).
	v, err, shared := s.group.Do(key, func() (any, error) {
		// Hàm này chạy với context của request đến đầu tiên. Request đó bị hủy thì các request
		// đang chờ cũng nhận lỗi theo, nên bỏ phần hủy đi; store PostgreSQL tự có timeout 3s mỗi truy vấn.
		ctx := context.WithoutCancel(ctx)
		v, err := load(ctx)
		if err != nil {
			return v, err // lỗi (kể cả ErrNotFound) không được cache
		}
		if cacheDown {
			return v, nil // vừa đọc lỗi thì ghi cũng lỗi: đừng bắt request chờ timeout thêm lần nữa
		}
		if b, err := json.Marshal(v); err == nil {
			if err := s.cache.Set(ctx, key, b, s.jitter()); err != nil {
				s.fails.Add(1)
				log.Warn("cache: ghi lỗi", "key", key, "err", err)
			}
		}
		return v, nil
	})
	log.Debug("cache miss", "key", key, "shared", shared)
	if err != nil {
		var zero T
		return zero, err
	}
	return v.(T), nil
}

// jitter trả về TTL cộng thêm tới 10% ngẫu nhiên, để các key được ghi cùng lúc
// (VD ngay sau khi deploy) không hết hạn cùng lúc rồi cùng đổ về database.
func (s *CachedStore) jitter() time.Duration {
	return s.ttl + rand.N(s.ttl/10+1)
}

// invalidate xóa các key sau khi database đã ghi xong. Xóa lỗi thì dữ liệu cũ còn trong cache
// tới khi hết TTL: chỉ ghi log, không báo lỗi cho request vì database đã ghi thành công.
func (s *CachedStore) invalidate(ctx context.Context, keys ...string) {
	// client ngắt kết nối ngay sau khi ghi xong thì vẫn phải xóa: bỏ phần hủy của context
	ctx = context.WithoutCancel(ctx)
	if err := s.cache.Delete(ctx, keys...); err != nil {
		s.fails.Add(1)
		reqlog.Logger(ctx).Warn("cache: xóa lỗi, dữ liệu cũ còn tới khi hết TTL", "keys", keys, "err", err)
		return
	}
	reqlog.Logger(ctx).Debug("cache: đã xóa", "keys", keys)
}
