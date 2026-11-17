package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"github.com/toidicode/taskflow/api"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
)

// TestContract gọi mọi operation trong openapi.yaml ít nhất một lần, cả các response lỗi
// chính, qua contract: tài liệu thiếu route, thiếu status code hay sai schema đều làm test lỗi.
// TestEndToEnd cũng đi qua contract với cả bốn backend.
func TestContract(t *testing.T) {
	if testing.Short() {
		t.Skip("chậm (bcrypt), bỏ qua khi chạy go test -short")
	}
	captureLog(t)
	a := withTestJobs(t, memoryApp(newTestFiles(t)))
	a.rateLimit = rateLimit(nil, e2eLimits)
	srv := httptest.NewServer(a.handler(testTokens, "", nil))
	t.Cleanup(srv.Close)
	ct := checkContract(t, srv)
	const jsonType = "application/json"

	anon := &client{t: t, srv: srv}
	anon.do("GET", "/health", "", nil, http.StatusOK)
	anon.do("GET", "/ready", "", nil, http.StatusOK)
	an := signUp(t, srv, "An", "an@example.com", "mat-khau-cua-an")
	binh := signUp(t, srv, "Bình", "binh@example.com", "mat-khau-cua-binh")
	call[struct{}](an, "GET", "/api/v1/me", nil, http.StatusOK)
	anon.do("GET", "/api/v1/me", "", nil, http.StatusUnauthorized)
	call[struct{}](anon, "POST", "/api/v1/auth/register",
		map[string]string{"email": "an@example.com", "name": "An", "password": "mat-khau-moi"}, http.StatusConflict)
	call[struct{}](anon, "POST", "/api/v1/auth/register", map[string]string{"email": "x", "name": "", "password": "1"},
		http.StatusUnprocessableEntity)
	anon.do("POST", "/api/v1/auth/register", "text/plain", strings.NewReader("an"), http.StatusUnsupportedMediaType)

	p := call[project.Project](an, "POST", "/api/v1/projects", map[string]any{"name": "TaskFlow API"}, http.StatusCreated)
	pp := fmt.Sprintf("/api/v1/projects/%d", p.ID)
	an.do("POST", "/api/v1/projects", jsonType, strings.NewReader(`{"name": `), http.StatusBadRequest)
	big := `{"name": "` + strings.Repeat("a", 1<<20) + `"}`
	an.do("POST", "/api/v1/projects", jsonType, strings.NewReader(big), http.StatusRequestEntityTooLarge)
	call[[]project.Project](an, "GET", "/api/v1/projects", nil, http.StatusOK)
	call[project.Project](an, "GET", pp, nil, http.StatusOK)
	an.do("GET", "/api/v1/projects/abc", "", nil, http.StatusBadRequest)
	binh.do("GET", pp, "", nil, http.StatusNotFound) // không phải project của Bình
	call[project.Project](an, "PUT", pp, map[string]any{"name": "TaskFlow API v2", "description": "Bài 45"}, http.StatusOK)

	tk := call[task.Task](an, "POST", "/api/v1/tasks", map[string]any{"project_id": p.ID, "title": "Viết openapi.yaml",
		"assignee_id": 2, "due_date": "2026-12-01T17:00:00Z", "tags": []string{"Docs"}}, http.StatusCreated)
	tp := fmt.Sprintf("/api/v1/tasks/%d", tk.ID)
	call[page[task.Task]](an, "GET", pp+"/tasks?limit=1", nil, http.StatusOK)
	call[page[task.Task]](binh, "GET", "/api/v1/tasks?status=todo&tag=docs", nil, http.StatusOK)
	an.do("GET", "/api/v1/tasks?limit=0", "", nil, http.StatusBadRequest)
	call[task.Task](binh, "GET", tp, nil, http.StatusOK)
	call[struct{}](binh, "PATCH", tp, map[string]any{"title": "Đổi tên"}, http.StatusForbidden) // người được giao chỉ đổi status
	call[task.Task](an, "PATCH", tp, map[string]any{"status": "doing", "due_date": nil}, http.StatusOK)
	call[task.Task](an, "POST", tp+"/tags", map[string]string{"name": "Backend"}, http.StatusOK)
	call[struct{}](an, "POST", tp+"/tags", map[string]string{"name": " "}, http.StatusUnprocessableEntity)
	an.do("DELETE", tp+"/tags/backend", "", nil, http.StatusNoContent)

	upload := binh.do("POST", tp+"/attachments", "multipart/form-data; boundary="+testBoundary,
		strings.NewReader(uploadBody("file", "ghi-chu.txt", "OpenAPI 3.1\n")), http.StatusCreated)
	an.do("POST", tp+"/attachments", jsonType, strings.NewReader("{}"), http.StatusUnsupportedMediaType)
	call[[]struct{}](an, "GET", tp+"/attachments", nil, http.StatusOK)
	var att struct{ ID int }
	if err := json.Unmarshal(upload, &att); err != nil {
		t.Fatal(err)
	}
	an.do("GET", fmt.Sprintf("/api/v1/attachments/%d", att.ID), "", nil, http.StatusOK)
	an.do("DELETE", fmt.Sprintf("/api/v1/attachments/%d", att.ID), "", nil, http.StatusNoContent)

	an.do("DELETE", pp, "", nil, http.StatusConflict) // còn task
	binh.do("DELETE", tp, "", nil, http.StatusForbidden)
	an.do("DELETE", tp, "", nil, http.StatusNoContent)
	an.do("DELETE", pp, "", nil, http.StatusNoContent)

	guess := map[string]string{"email": "an@example.com", "password": "doan-mo"}
	call[struct{}](anon, "POST", "/api/v1/auth/login", guess, http.StatusUnauthorized)
	call[struct{}](anon, "POST", "/api/v1/auth/login", guess, http.StatusTooManyRequests) // 2 lần mỗi email: An đã đăng nhập một lần

	doc, _ := loadSpec(t)
	if m := ct.missing(doc); len(m) > 0 {
		t.Errorf("các operation chưa được gọi lần nào: %v", m)
	}
}

// loadSpec đọc và validate api/openapi.yaml, rồi tạo router tìm operation của một request.
func loadSpec(t *testing.T) (*openapi3.T, routers.Router) {
	t.Helper()
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(api.Spec)
	if err != nil {
		t.Fatalf("openapi.yaml: %v", err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		t.Fatalf("openapi.yaml không hợp lệ: %v", err)
	}
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}
	return doc, router
}

// contract là http.RoundTripper kiểm tra mọi request đi qua và response của nó với openapi.yaml:
// route phải có trong tài liệu, status code phải được khai báo, body và header phải khớp schema.
type contract struct {
	t      *testing.T
	router routers.Router
	next   http.RoundTripper

	mu   sync.Mutex
	seen map[string]bool // "POST /api/v1/tasks" của các operation đã gọi
}

// checkContract cho client của srv (srv.Client(), e2e dùng nó) đi qua contract.
func checkContract(t *testing.T, srv *httptest.Server) *contract {
	t.Helper()
	_, router := loadSpec(t)
	hc := srv.Client()
	c := &contract{t: t, router: router, next: hc.Transport, seen: map[string]bool{}}
	hc.Transport = c
	return c
}

func (c *contract) RoundTrip(req *http.Request) (*http.Response, error) {
	var reqBody []byte
	if req.Body != nil { // đọc hết body để vừa gửi đi vừa kiểm tra được
		var err error
		if reqBody, err = io.ReadAll(req.Body); err != nil {
			return nil, err
		}
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}
	resp, err := c.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(respBody))

	c.check(req, reqBody, resp, respBody)
	return resp, nil
}

// check tìm operation của req trong tài liệu rồi validate request và response.
func (c *contract) check(req *http.Request, reqBody []byte, resp *http.Response, respBody []byte) {
	c.t.Helper()
	name := req.Method + " " + req.URL.Path
	route, params, err := c.router.FindRoute(req)
	if err != nil {
		c.t.Errorf("contract: %s: không có trong openapi.yaml: %v", name, err)
		return
	}
	c.mu.Lock()
	c.seen[req.Method+" "+route.Path] = true
	c.mu.Unlock()

	in := &openapi3filter.RequestValidationInput{
		Request:    req.Clone(context.Background()),
		PathParams: params,
		Route:      route,
		Options:    &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
	}
	in.Request.Body = io.NopCloser(bytes.NewReader(reqBody))
	// Request thành công thì phải đúng tài liệu: tài liệu chặt hơn server là tài liệu sai.
	// Request bị từ chối (4xx) thường là cố ý gửi sai, nên chỉ kiểm tra response của nó.
	if resp.StatusCode < 400 {
		if err := openapi3filter.ValidateRequest(context.Background(), in); err != nil {
			c.t.Errorf("contract: request %s: %v", name, err)
		}
	}

	out := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: in,
		Status:                 resp.StatusCode,
		Header:                 resp.Header,
		Body:                   io.NopCloser(bytes.NewReader(respBody)),
		Options:                &openapi3filter.Options{IncludeResponseStatus: true}, // status lạ cũng là lỗi
	}
	if err := openapi3filter.ValidateResponse(context.Background(), out); err != nil {
		c.t.Errorf("contract: response %s %d: %v", name, resp.StatusCode, err)
	}
}

// CloseIdleConnections chuyển tiếp cho transport bên dưới: srv.Close gọi nó.
func (c *contract) CloseIdleConnections() {
	if ci, ok := c.next.(interface{ CloseIdleConnections() }); ok {
		ci.CloseIdleConnections()
	}
}

// missing trả về các operation trong doc chưa được gọi lần nào, VD "DELETE /api/v1/attachments/{id}".
func (c *contract) missing(doc *openapi3.T) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for path, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			if !c.seen[method+" "+path] {
				out = append(out, method+" "+path)
			}
		}
	}
	slices.Sort(out)
	return out
}
