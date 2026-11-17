// Package api chứa tài liệu OpenAPI của TaskFlow API (openapi.yaml), viết tay và nhúng vào binary.
// Đây là nguồn sự thật về hình dạng của API: test đối chiếu route và response thật với file này.
package api

import _ "embed"

// Spec là nội dung openapi.yaml.
//
//go:embed openapi.yaml
var Spec []byte
