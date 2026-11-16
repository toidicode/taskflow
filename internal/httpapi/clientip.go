package httpapi

import (
	"fmt"
	"net/netip"
	"strings"
)

// ParseTrustedProxies đọc danh sách proxy tin cậy dạng "10.0.0.5, 10.39.0.0/24":
// mỗi phần tử là một IP hoặc một dải CIDR. Chuỗi rỗng là không tin proxy nào.
// IP thật của client do c.ClientIP() của Gin tính, với danh sách này (xem newEngine).
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
