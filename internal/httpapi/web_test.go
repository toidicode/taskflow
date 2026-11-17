package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/toidicode/taskflow/internal/auth"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/user"
)

const webPassword = "mat-khau-cua-an"

// newWebTest tạo engine có dashboard với dữ liệu mẫu, user #1 (An) đăng nhập được bằng webPassword.
func newWebTest(t *testing.T) (http.Handler, stores) {
	t.Helper()
	s := withTestJobs(t, memoryStores(newTestFiles(t)))
	hash, err := auth.HashPassword(webPassword)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.users.Add(ctx, &user.User{Email: "an@example.com", Name: "An", PasswordHash: hash}); err != nil {
		t.Fatal(err)
	}
	if err := s.users.Add(ctx, &user.User{Email: "binh@example.com", Name: "Bình"}); err != nil {
		t.Fatal(err)
	}
	if err := seed(ctx, s.projects, s.tasks); err != nil {
		t.Fatal(err)
	}
	return newMux(s, testTokens), s
}

// sessionFor là cookie session của user userID, như sau khi đăng nhập.
func sessionFor(t *testing.T, userID int) *http.Cookie {
	t.Helper()
	token, err := testTokens.ForAudience(sessionAudience, sessionTTL).Issue(userID)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookie, Value: token}
}

// webDo gửi request tới dashboard. form khác nil thì gửi dạng application/x-www-form-urlencoded;
// header là các cặp tên, giá trị.
func webDo(h http.Handler, method, target string, form url.Values, cookie *http.Cookie, header ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestWebLogin(t *testing.T) {
	h, _ := newWebTest(t)

	rec := webDo(h, "GET", "/app", nil, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/app/login" {
		t.Fatalf("chưa đăng nhập: %d %q, muốn 303 tới /app/login", rec.Code, rec.Header().Get("Location"))
	}

	rec = webDo(h, "POST", "/app/login", url.Values{"email": {"An@Example.com"}, "password": {"sai-mat-khau"}}, nil)
	body := rec.Body.String()
	if rec.Code != http.StatusUnauthorized || !strings.Contains(body, `value="An@Example.com"`) ||
		strings.Contains(body, "sai-mat-khau") || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("sai mật khẩu: %d, muốn 401, form giữ email, không có mật khẩu, không có cookie\n%s", rec.Code, body)
	}

	rec = webDo(h, "POST", "/app/login", url.Values{"email": {"an@example.com"}, "password": {webPassword}}, nil)
	cookies := rec.Result().Cookies()
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/app" || len(cookies) != 1 {
		t.Fatalf("đăng nhập: %d %q %v", rec.Code, rec.Header().Get("Location"), cookies)
	}
	session := cookies[0]
	if session.Name != sessionCookie || !session.HttpOnly || !session.Secure ||
		session.SameSite != http.SameSiteLaxMode || session.Path != "/app" || session.MaxAge != 12*60*60 {
		t.Errorf("cookie: %+v", session)
	}

	rec = webDo(h, "GET", "/app", nil, session)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `<a href="/app/projects/1">TaskFlow API</a>`) {
		t.Errorf("/app sau khi đăng nhập: %d\n%s", rec.Code, rec.Body)
	}

	// session và access token có aud khác nhau: không dùng lẫn được
	apiToken, _ := testTokens.Issue(1)
	if rec := webDo(h, "GET", "/app", nil, &http.Cookie{Name: sessionCookie, Value: apiToken}); rec.Code != http.StatusSeeOther {
		t.Errorf("access token làm cookie session: %d, muốn 303", rec.Code)
	}
	if rec := webDo(h, "GET", "/api/v1/me", nil, nil, "Authorization", "Bearer "+session.Value); rec.Code != http.StatusUnauthorized {
		t.Errorf("cookie session làm Bearer token: %d, muốn 401", rec.Code)
	}

	rec = webDo(h, "POST", "/app/logout", url.Values{}, session)
	if c := rec.Result().Cookies(); rec.Code != http.StatusSeeOther || len(c) != 1 || c[0].MaxAge >= 0 {
		t.Errorf("đăng xuất: %d %v, muốn 303 và cookie bị xóa", rec.Code, c)
	}
}

func TestWebProjectPage(t *testing.T) {
	h, s := newWebTest(t)
	an := sessionFor(t, 1)

	rec := webDo(h, "GET", "/app/projects/1", nil, an)
	body := rec.Body.String()
	for _, want := range []string{
		"<title>TaskFlow API - TaskFlow</title>",
		`<tr id="task-2">`,
		"Viết API danh sách task",
		`<option value="doing" selected>Đang làm</option>`,
		`<form method="post" action="/app/projects/1/tasks">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("trang project thiếu %q", want)
		}
	}
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("trang project: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if csp := rec.Header().Get("Content-Security-Policy"); csp != webCSP || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP: %q", csp)
	}

	// Bình không phải chủ project: 404 như project không tồn tại (Bài 34)
	if rec := webDo(h, "GET", "/app/projects/1", nil, sessionFor(t, 2)); rec.Code != http.StatusNotFound {
		t.Errorf("project của người khác: %d, muốn 404", rec.Code)
	}
	for _, target := range []string{"/app/projects/99", "/app/projects/abc"} {
		if rec := webDo(h, "GET", target, nil, an); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, muốn 404", target, rec.Code)
		}
	}

	// tiêu đề task chứa HTML: html/template escape, trình duyệt hiện nguyên văn chứ không chạy script
	xss := &task.Task{ProjectID: 1, Title: `<script>alert("xss")</script>`, Tags: []string{`"><img src=x onerror=alert(1)>`}}
	if err := s.tasks.Add(context.Background(), xss); err != nil {
		t.Fatal(err)
	}
	body = webDo(h, "GET", "/app/projects/1", nil, an).Body.String()
	if !strings.Contains(body, `&lt;script&gt;alert(&#34;xss&#34;)&lt;/script&gt;`) ||
		strings.Contains(body, `<script>alert`) || strings.Contains(body, `<img src=x`) {
		t.Errorf("XSS không được escape:\n%s", body)
	}
}

func TestWebCreateTask(t *testing.T) {
	h, s := newWebTest(t)
	an := sessionFor(t, 1)

	rec := webDo(h, "POST", "/app/projects/2/tasks", url.Values{"title": {"Viết trang dashboard"}, "due_date": {"2026-12-01"}}, an)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/app/projects/2" {
		t.Fatalf("thêm task: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	got, err := s.tasks.Get(context.Background(), 5)
	if err != nil || got.Title != "Viết trang dashboard" || got.DueDate.Format("2006-01-02") != "2026-12-01" {
		t.Errorf("task mới: %+v %v", got, err)
	}

	// dữ liệu sai: 422, trang có lỗi từng field và giữ giá trị vừa nhập
	rec = webDo(h, "POST", "/app/projects/2/tasks", url.Values{"title": {"   "}, "due_date": {"2026-12-01"}}, an)
	body := rec.Body.String()
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, `<p class="error">không được để trống</p>`) ||
		!strings.Contains(body, `name="due_date" value="2026-12-01"`) {
		t.Errorf("tiêu đề trống: %d\n%s", rec.Code, body)
	}
	rec = webDo(h, "POST", "/app/projects/2/tasks", url.Values{"title": {`Tiêu đề "có ngoặc"`}, "due_date": {"01/12/2026"}}, an)
	body = rec.Body.String()
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "cần một ngày dạng YYYY-MM-DD") ||
		!strings.Contains(body, `value="Tiêu đề &#34;có ngoặc&#34;"`) {
		t.Errorf("ngày sai: %d\n%s", rec.Code, body)
	}
	if rec := webDo(h, "POST", "/app/projects/1/tasks", url.Values{"title": {"x"}}, sessionFor(t, 2)); rec.Code != http.StatusNotFound {
		t.Errorf("thêm task vào project người khác: %d, muốn 404", rec.Code)
	}
}

func TestWebCrossOrigin(t *testing.T) {
	h, s := newWebTest(t)
	an := sessionFor(t, 1)
	form := url.Values{"title": {"Task do trang khác gửi"}}

	for _, header := range [][]string{
		{"Sec-Fetch-Site", "cross-site"},
		{"Sec-Fetch-Site", "same-site"}, // subdomain khác cũng bị chặn
		{"Origin", "https://evil.example"},
	} {
		rec := webDo(h, "POST", "/app/projects/1/tasks", form, an, header...)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "gửi từ trang web khác") {
			t.Errorf("%v: %d, muốn 403", header, rec.Code)
		}
	}
	if list, _ := s.tasks.List(context.Background(), task.Filter{}); len(list) != 4 {
		t.Fatalf("có %d task, request bị chặn không được tạo task", len(list))
	}

	// cùng origin: Sec-Fetch-Site same-origin, hoặc Origin khớp Host (trình duyệt cũ)
	if rec := webDo(h, "POST", "/app/projects/1/tasks", form, an, "Sec-Fetch-Site", "same-origin"); rec.Code != http.StatusSeeOther {
		t.Errorf("same-origin: %d, muốn 303", rec.Code)
	}
	if rec := webDo(h, "POST", "/app/projects/1/tasks", form, an, "Origin", "http://example.com"); rec.Code != http.StatusSeeOther {
		t.Errorf("Origin khớp Host: %d, muốn 303", rec.Code) // Host của httptest.NewRequest là example.com
	}
	// GET không bị kiểm tra: GET không được thay đổi dữ liệu
	if rec := webDo(h, "GET", "/app", nil, an, "Sec-Fetch-Site", "cross-site"); rec.Code != http.StatusOK {
		t.Errorf("GET cross-site: %d, muốn 200", rec.Code)
	}
}

func TestWebSetStatus(t *testing.T) {
	h, _ := newWebTest(t)
	an := sessionFor(t, 1)

	// htmx: nhận lại đúng một dòng task
	rec := webDo(h, "POST", "/app/tasks/3/status", url.Values{"status": {"done"}}, an, "HX-Request", "true")
	body := strings.TrimSpace(rec.Body.String())
	if rec.Code != http.StatusOK || !strings.HasPrefix(body, `<tr id="task-3">`) || strings.Contains(body, "<html") ||
		!strings.Contains(body, `<option value="done" selected>Xong</option>`) {
		t.Errorf("htmx: %d\n%s", rec.Code, body)
	}
	// form thường: Post/Redirect/Get
	rec = webDo(h, "POST", "/app/tasks/3/status", url.Values{"status": {"doing"}}, an)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/app/projects/1" {
		t.Errorf("form: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := webDo(h, "POST", "/app/tasks/3/status", url.Values{"status": {"xong"}}, an); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status sai: %d, muốn 422", rec.Code)
	}
	if rec := webDo(h, "POST", "/app/tasks/3/status", url.Values{"status": {"done"}}, sessionFor(t, 2)); rec.Code != http.StatusNotFound {
		t.Errorf("task của người khác: %d, muốn 404", rec.Code)
	}
}

// TestPagesReload: chế độ dev đọc template từ thư mục, mọi trang đều parse được.
func TestPagesReload(t *testing.T) {
	p, err := newPages("web")
	if err != nil || !p.reload {
		t.Fatalf("newPages: %v", err)
	}
	for _, name := range []string{"login", "projects", "project", "error"} {
		if p.parsed.byName[name] == nil {
			t.Errorf("thiếu trang %s", name)
		}
	}
	if _, err := newPages(t.TempDir()); err == nil {
		t.Error("thư mục không có template: muốn lỗi")
	}
}
