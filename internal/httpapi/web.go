package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/toidicode/taskflow/internal/auth"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/user"
)

// Dashboard HTML dưới /app (Bài 48): trang render ở server bằng html/template, đăng nhập bằng
// cookie session, dùng chung service với JSON API. Không phải một phần của API nên không có
// trong openapi.yaml.

const (
	sessionCookie   = "taskflow_session"
	sessionAudience = "taskflow-web" // aud của token session, khác "taskflow-api" của access token
	sessionTTL      = 12 * time.Hour
	maxFormBytes    = 64 << 10 // form của dashboard đều nhỏ
	htmxURL         = "https://cdn.jsdelivr.net/npm/htmx.org@2.0.11/dist/htmx.min.js"
)

// webCSP chỉ cho trang chạy htmx từ jsDelivr (layout.html có kèm integrity), CSS cùng origin,
// không có script hay style inline nào; form chỉ gửi về chính site, không cho nhúng vào iframe.
var webCSP = strings.Join([]string{
	"default-src 'none'",
	"script-src " + htmxURL,
	"style-src 'self'",
	"img-src 'self' data:", // favicon rỗng data:, trong layout
	"connect-src 'self'",   // htmx gửi request về cùng origin
	"form-action 'self'",
	"frame-ancestors 'none'",
	"base-uri 'none'",
}, "; ")

// view là dữ liệu mọi trang nhận được, trang nào dùng field nào thì điền field đó.
type view struct {
	User     user.User  // user đang đăng nhập, ID 0 là chưa đăng nhập
	Message  string     // thông báo chung: đăng nhập sai, trang lỗi
	Form     url.Values // giá trị vừa gửi, để điền lại vào form khi có lỗi
	Errors   validator  // lỗi theo field (Bài 27), cùng tên field với API
	Projects []project.Project
	Project  project.Project
	Tasks    []task.Task
}

// webRoutes đăng ký dashboard. Template lỗi là lỗi lập trình: panic ngay lúc khởi động.
func webRoutes(r *gin.Engine, d Deps) {
	p, err := newPages(d.WebDir)
	if err != nil {
		panic(fmt.Sprintf("template của dashboard: %v", err))
	}
	sessions := d.Tokens.ForAudience(sessionAudience, sessionTTL)
	rl := d.RateLimit

	app := r.Group("/app", securityHeaders, crossOriginProtection(p), limitForm)
	get(app, "/static/app.css", appCSS)
	get(app, "/login", func(c *gin.Context) { p.page(c, http.StatusOK, "login", view{}) })
	app.POST("/login", rl.middleware(policy{"login-ip", rl.Limits.AuthIP, false}, ipKey), webLogin(p, d.Users, sessions, rl))
	app.POST("/logout", webLogout)

	in := app.Group("", requireSession(sessions, d.Users))
	get(in, "", projectsPage(p, d.Projects))
	get(in, "/projects/:id", projectPage(p, d))
	in.POST("/projects/:id/tasks", webCreateTask(p, d))
	in.POST("/tasks/:id/status", webSetStatus(p, d.Tasks))
}

// securityHeaders: CSP như trang /docs (Bài 45), cộng các header nên có cho mọi trang HTML.
func securityHeaders(c *gin.Context) {
	h := c.Writer.Header()
	h.Set("Content-Security-Policy", webCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Cache-Control", "no-store") // trang có dữ liệu riêng: không lưu cache, kể cả nút Back sau khi đăng xuất
}

// crossOriginProtection chặn CSRF bằng http.CrossOriginProtection (Go 1.25): POST từ trang
// web khác (Sec-Fetch-Site: cross-site, hoặc Origin khác Host) bị từ chối 403.
func crossOriginProtection(p *pages) gin.HandlerFunc {
	cop := http.NewCrossOriginProtection()
	return func(c *gin.Context) {
		if err := cop.Check(c.Request); err != nil {
			requestLogger(c).Warn("chặn request từ origin khác", "err", err)
			p.page(c, http.StatusForbidden, "error", view{Message: "Request bị từ chối: gửi từ trang web khác."})
			c.Abort()
		}
	}
}

// limitForm giới hạn kích thước body của form trước khi Gin đọc (PostForm).
func limitForm(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxFormBytes)
}

func appCSS(c *gin.Context) {
	css, err := webFS.ReadFile("web/static/app.css")
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "public, max-age=3600") // đè no-store: CSS không có dữ liệu riêng
	c.Data(http.StatusOK, "text/css; charset=utf-8", css)
}

// webLogin kiểm tra email, mật khẩu như POST /api/v1/auth/login (cùng hàm authenticate, cùng
// giới hạn theo email), đúng thì đặt cookie session rồi chuyển về /app.
func webLogin(p *pages, users user.Store, sessions *auth.Tokens, rl RateLimit) gin.HandlerFunc {
	emailPolicy := policy{"login-email", rl.Limits.LoginEmail, false}
	return func(c *gin.Context) {
		email := user.NormalizeEmail(c.PostForm("email"))
		if !rl.allow(c, emailPolicy, rl.emailKey(email)) {
			return
		}
		u, ok, err := authenticate(c.Request.Context(), users, email, c.PostForm("password"))
		if err != nil {
			webError(c, p, err)
			return
		}
		if !ok { // hiện lại form với email vừa nhập, không bao giờ điền lại mật khẩu
			form := url.Values{"email": {c.PostForm("email")}}
			p.page(c, http.StatusUnauthorized, "login", view{Form: form, Message: "Email hoặc mật khẩu không đúng."})
			return
		}
		token, err := sessions.Issue(u.ID)
		if err != nil {
			webError(c, p, err)
			return
		}
		setSessionCookie(c, token, int(sessionTTL.Seconds()))
		c.Redirect(http.StatusSeeOther, "/app")
	}
}

// webLogout xóa cookie. Token session vẫn còn hạn tới lúc hết TTL: ai đã chép được nó thì vẫn
// dùng được, muốn thu hồi thật phải lưu session ở server.
func webLogout(c *gin.Context) {
	setSessionCookie(c, "", -1)
	c.Redirect(http.StatusSeeOther, "/app/login")
}

// setSessionCookie ghi cookie session; maxAge < 0 là xóa cookie.
func setSessionCookie(c *gin.Context, value string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/app",               // API dưới /api/v1 không nhận cookie này, chỉ nhận Bearer token
		MaxAge:   maxAge,               // giây
		HttpOnly: true,                 // JavaScript (kể cả script bị chèn vào trang) không đọc được
		Secure:   true,                 // chỉ gửi qua HTTPS; trình duyệt coi http://localhost là an toàn
		SameSite: http.SameSiteLaxMode, // không gửi kèm POST từ site khác
	})
}

// requireSession chỉ cho request có cookie session hợp lệ đi tiếp, không có thì chuyển về trang
// đăng nhập. User được đọc lại từ store: user bị xóa thì session hết tác dụng ngay.
func requireSession(sessions *auth.Tokens, users user.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(sessionCookie)
		if err == nil {
			var id int
			if id, err = sessions.Verify(token); err == nil {
				var u user.User
				if u, err = users.Get(c.Request.Context(), id); err == nil {
					c.Set(userIDKey, u.ID)
					c.Set(webUserKey, u)
					c.Next()
					return
				}
			}
		}
		requestLogger(c).Debug("không có session hợp lệ", "err", err)
		c.Redirect(http.StatusSeeOther, "/app/login")
		c.Abort()
	}
}

const webUserKey = "web_user"

// currentUser trả về user mà requireSession đã đọc.
func currentUser(c *gin.Context) user.User {
	u, _ := c.Get(webUserKey)
	return u.(user.User)
}

// projectsPage: danh sách project của user.
func projectsPage(p *pages, projects *project.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		list, err := projects.List(c.Request.Context(), userIDFrom(c))
		if err != nil {
			webError(c, p, err)
			return
		}
		p.page(c, http.StatusOK, "projects", view{User: currentUser(c), Projects: list})
	}
}

// projectView đọc project :id (kiểm tra quyền trong project.Service) và các task của nó.
func projectView(c *gin.Context, d Deps) (view, error) {
	id, ok := webPathID(c)
	if !ok {
		return view{}, fmt.Errorf("project %q: %w", c.Param("id"), project.ErrNotFound)
	}
	ctx, uid := c.Request.Context(), userIDFrom(c)
	pr, err := d.Projects.Get(ctx, uid, id)
	if err != nil {
		return view{}, err
	}
	tasks, err := d.Tasks.List(ctx, uid, task.Filter{ProjectID: id})
	if err != nil {
		return view{}, err
	}
	return view{User: currentUser(c), Project: pr, Tasks: tasks}, nil
}

// projectPage: project kèm task và form thêm task.
func projectPage(p *pages, d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		v, err := projectView(c, d)
		if err != nil {
			webError(c, p, err)
			return
		}
		p.page(c, http.StatusOK, "project", v)
	}
}

// webCreateTask thêm task từ form. Dữ liệu sai thì render lại trang project với 422, giá trị
// vừa nhập và lỗi từng field; đúng thì 303 về trang project (Post/Redirect/Get).
func webCreateTask(p *pages, d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		v, err := projectView(c, d)
		if err != nil {
			webError(c, p, err)
			return
		}
		t := &task.Task{ProjectID: v.Project.ID, Title: c.PostForm("title")}
		v.Errors = validator{}
		if s := c.PostForm("due_date"); s != "" {
			due, err := time.ParseInLocation(time.DateOnly, s, time.Local) // <input type="date">: 2006-01-02
			v.Errors.check(err == nil, "due_date", "cần một ngày dạng YYYY-MM-DD")
			t.DueDate = due
		}
		if len(v.Errors) == 0 {
			err := d.Tasks.Create(c.Request.Context(), userIDFrom(c), t) // luật validate nằm trong service
			if err = v.Errors.addError(err); err != nil {
				webError(c, p, err)
				return
			}
		}
		if len(v.Errors) > 0 {
			v.Form = c.Request.PostForm
			p.page(c, http.StatusUnprocessableEntity, "project", v)
			return
		}
		c.Redirect(http.StatusSeeOther, fmt.Sprintf("/app/projects/%d", v.Project.ID))
	}
}

// webSetStatus đổi status của task :id. Request của htmx (header HX-Request) nhận lại đúng một
// dòng task (partial task-row) để thay vào bảng; form thường thì 303 về trang project.
func webSetStatus(p *pages, tasks *task.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := webPathID(c)
		if !ok {
			webError(c, p, fmt.Errorf("task %q: %w", c.Param("id"), task.ErrNotFound))
			return
		}
		status, err := task.ParseStatus(c.PostForm("status"))
		if err != nil {
			p.page(c, http.StatusUnprocessableEntity, "error", view{User: currentUser(c), Message: "Status không hợp lệ."})
			return
		}
		t, err := tasks.Update(c.Request.Context(), userIDFrom(c), id, task.Changes{Status: &status})
		if err != nil {
			webError(c, p, err)
			return
		}
		if c.GetHeader("HX-Request") == "true" {
			p.partial(c, http.StatusOK, "task-row", t)
			return
		}
		c.Redirect(http.StatusSeeOther, fmt.Sprintf("/app/projects/%d", t.ProjectID))
	}
}

// webPathID đọc :id, không ghi response như pathID của API.
func webPathID(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	return id, err == nil && id > 0
}

// webError là writeError của dashboard: cùng cách đổi lỗi domain sang status code, nhưng trả trang HTML.
func webError(c *gin.Context, p *pages, err error) {
	v := view{Message: "Lỗi máy chủ, hãy thử lại sau."}
	if u, ok := c.Get(webUserKey); ok {
		v.User = u.(user.User)
	}
	status := http.StatusInternalServerError
	var forbidden *task.ForbiddenError
	switch {
	case errors.Is(err, task.ErrNotFound) || errors.Is(err, project.ErrNotFound):
		status, v.Message = http.StatusNotFound, "Không tìm thấy trang này."
	case errors.As(err, &forbidden):
		status, v.Message = http.StatusForbidden, forbidden.Reason
	default:
		requestLogger(c).Error("dashboard", "err", err)
	}
	p.page(c, status, "error", v)
}
