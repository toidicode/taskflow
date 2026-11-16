package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

// ParseTrustedProxies đọc danh sách proxy tin cậy dạng "10.0.0.5, 10.39.0.0/24":
// mỗi phần tử là một IP hoặc một dải CIDR. Chuỗi rỗng là không tin proxy nào.
func ParseTrustedProxies(s string) ([]netip.Prefix, error) {
	var res []netip.Prefix
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if p, err := netip.ParsePrefix(part); err == nil {
			res = append(res, p.Masked())
			continue
		}
		ip, err := netip.ParseAddr(part)
		if err != nil {
			return nil, fmt.Errorf("%q: cần một IP hoặc dải CIDR như 10.39.0.0/24", part)
		}
		res = append(res, netip.PrefixFrom(ip, ip.BitLen())) // một IP = dải chỉ có nó
	}
	return res, nil
}

// clientIP gắn IP của client vào context để log và các middleware sau dùng.
func clientIP(trusted []netip.Prefix) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), clientIPKey, realIP(r, trusted))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// realIP trả về IP của client. Mặc định là IP của kết nối TCP (r.RemoteAddr). Chỉ khi kết nối
// đến từ một proxy tin cậy, IP mới được lấy từ X-Forwarded-For: header này ai cũng tự gửi được.
func realIP(r *http.Request, trusted []netip.Prefix) string {
	addrPort, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	ip := addrPort.Addr().Unmap()
	// mỗi proxy nối IP mà nó thấy vào cuối header, nên đọc từ phải sang trái
	// và dừng ở IP đầu tiên không phải proxy tin cậy
	xff := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(xff) - 1; i >= 0 && isTrusted(ip, trusted); i-- {
		prev, err := netip.ParseAddr(strings.TrimSpace(xff[i]))
		if err != nil {
			break // header rỗng hoặc sai: giữ IP của proxy
		}
		ip = prev.Unmap()
	}
	return ip.String()
}

func isTrusted(ip netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIPFrom đọc IP client mà middleware clientIP đã gắn; không có thì dùng IP của kết nối.
func clientIPFrom(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey).(string); ok {
		return ip
	}
	return realIP(r, nil)
}
