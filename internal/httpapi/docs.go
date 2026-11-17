package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/toidicode/taskflow/api"
)

// swaggerUI là phiên bản Swagger UI mà trang /docs tải từ CDN jsDelivr. Đổi phiên bản thì phải
// tính lại hai giá trị integrity bên dưới (SRI): trình duyệt từ chối file có nội dung khác mã băm,
// nên CDN bị tấn công cũng không chèn được script lạ vào trang.
const (
	swaggerUI       = "https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.35.0/"
	swaggerUICSSSRI = "sha384-WlYgdmXYOOBWL5apwjDEZwN+U3HmXSSu5R6V5aGxEcf7z+KuRNwBhoxKVN1i3D5I"
	swaggerUIJSSRI  = "sha384-gjRm0bEPMeTmMh5jT2cHccIwCUZSTd8YC+j0d0sXw0MdGEfScduGpSyF3ZSSLkX9"
)

// docsScript khởi tạo Swagger UI với tài liệu ở /openapi.yaml (cùng origin).
const docsScript = `SwaggerUIBundle({url: "/openapi.yaml", dom_id: "#swagger-ui", validatorUrl: null});`

var docsPage = `<!doctype html>
<html lang="vi">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>TaskFlow API</title>
<link rel="icon" href="data:,">
<link rel="stylesheet" href="` + swaggerUI + `swagger-ui.css" integrity="` + swaggerUICSSSRI + `" crossorigin="anonymous">
</head>
<body>
<div id="swagger-ui"></div>
<script src="` + swaggerUI + `swagger-ui-bundle.js" integrity="` + swaggerUIJSSRI + `" crossorigin="anonymous"></script>
<script>` + docsScript + `</script>
</body>
</html>
`

// docsCSP chỉ cho trang /docs chạy script từ jsDelivr và đúng đoạn script inline ở trên (theo mã băm),
// chỉ gọi API cùng origin, và không cho trang khác nhúng vào iframe.
var docsCSP = strings.Join([]string{
	"default-src 'none'",
	"script-src " + swaggerUI + " 'sha256-" + sha256Base64(docsScript) + "'",
	"style-src " + swaggerUI + " 'unsafe-inline'", // Swagger UI đặt style inline cho vài phần tử
	"img-src 'self' data:",
	"connect-src 'self'",
	"frame-ancestors 'none'",
	"base-uri 'none'",
	"form-action 'none'",
}, "; ")

func sha256Base64(s string) string {
	sum := sha256.Sum256([]byte(s))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// openAPISpec trả về api/openapi.yaml (đã nhúng vào binary). application/yaml là media type
// chính thức của YAML (RFC 9512).
func openAPISpec(c *gin.Context) {
	c.Header("Cache-Control", "no-cache") // trình duyệt hỏi lại mỗi lần: deploy bản mới là thấy ngay
	c.Data(http.StatusOK, "application/yaml", api.Spec)
}

// docs trả về trang Swagger UI đọc /openapi.yaml.
func docs(c *gin.Context) {
	c.Header("Content-Security-Policy", docsCSP)
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(docsPage))
}
