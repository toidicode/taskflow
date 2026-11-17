package httpapi

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/toidicode/taskflow/api"
)

// TestOpenAPIRoutes so route đăng ký trong Gin với các operation trong api/openapi.yaml:
// thêm route mà quên viết tài liệu, hay tài liệu còn route đã xóa, đều làm test lỗi.
func TestOpenAPIRoutes(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromData(api.Spec)
	if err != nil {
		t.Fatal(err)
	}
	var documented []string
	for path, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			documented = append(documented, method+" "+path)
		}
	}

	notDocumented := map[string]bool{"GET /openapi.yaml": true, "GET /docs": true} // chính là tài liệu
	var routes []string
	for _, r := range newMux(newTestStores(t), testTokens).Routes() {
		if r.Method == http.MethodHead { // get() đăng ký HEAD kèm mọi GET
			continue
		}
		route := r.Method + " " + ginToOpenAPI(r.Path)
		if !notDocumented[route] {
			routes = append(routes, route)
		}
	}

	slices.Sort(documented)
	slices.Sort(routes)
	for _, r := range routes {
		if !slices.Contains(documented, r) {
			t.Errorf("route %s chưa có trong openapi.yaml", r)
		}
	}
	for _, d := range documented {
		if !slices.Contains(routes, d) {
			t.Errorf("openapi.yaml có %s nhưng không có route này", d)
		}
	}
}

// ginToOpenAPI đổi tham số trên path từ cách viết của Gin (/tasks/:id) sang OpenAPI (/tasks/{id}).
func ginToOpenAPI(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if name, ok := strings.CutPrefix(p, ":"); ok {
			parts[i] = "{" + name + "}"
		}
	}
	return strings.Join(parts, "/")
}

func TestDocs(t *testing.T) {
	h := newTestMux(t)

	rec := send(h, "GET", "/openapi.yaml", "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/yaml" || rec.Body.String() != string(api.Spec) {
		t.Errorf("/openapi.yaml: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}

	rec = send(h, "GET", "/docs", "")
	csp := rec.Header().Get("Content-Security-Policy")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `integrity="`+swaggerUIJSSRI+`"`) {
		t.Errorf("/docs: %d", rec.Code)
	}
	if !strings.Contains(csp, "'sha256-"+sha256Base64(docsScript)+"'") || !strings.Contains(rec.Body.String(), "<script>"+docsScript+"</script>") {
		t.Errorf("CSP %q không cho phép đúng script inline của trang", csp)
	}
}
