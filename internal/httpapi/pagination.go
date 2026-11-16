package httpapi

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
)

// Số phần tử mỗi trang: mặc định khi client không gửi ?limit= và tối đa client được xin.
const (
	defaultLimit = 20
	maxLimit     = 100
)

// page là body của các API trả về danh sách có phân trang.
// NextCursor rỗng (không có trong JSON) nghĩa là đây là trang cuối.
type page[T any] struct {
	Data       []T    `json:"data"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// parsePage đọc ?limit= và ?cursor= của query. Tham số sai thì ghi lỗi vào v.
// afterID là ID cuối của trang trước, 0 khi chưa có cursor (trang đầu).
func parsePage(query url.Values, v validator) (limit, afterID int) {
	limit = defaultLimit
	if s := query.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		v.check(err == nil && n >= 1 && n <= maxLimit, "limit", fmt.Sprintf("cần một số từ 1 tới %d", maxLimit))
		limit = n
	}
	if s := query.Get("cursor"); s != "" {
		id, err := decodeCursor(s)
		v.check(err == nil, "cursor", "cursor không hợp lệ")
		afterID = id
	}
	return limit, afterID
}

// encodeCursor đóng gói ID cuối của một trang thành chuỗi cursor. Client chỉ gửi lại
// nguyên chuỗi này, không cần biết bên trong là gì, nên sau này đổi cách sắp xếp
// (VD theo due_date rồi id) thì chỉ đổi nội dung cursor, API giữ nguyên.
func encodeCursor(id int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(id)))
}

// decodeCursor làm ngược lại encodeCursor.
func decodeCursor(s string) (int, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return 0, err
	}
	id, err := strconv.Atoi(string(b))
	if err != nil || id < 1 {
		return 0, fmt.Errorf("cursor %q không hợp lệ", s)
	}
	return id, nil
}
