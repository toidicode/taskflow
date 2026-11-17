// Package httpapi là tầng HTTP của TaskFlow API, viết bằng Gin: route, middleware, đọc request và ghi response.
// Luật nghiệp vụ nằm trong các service (task.Service, project.Service), handler chỉ đọc input,
// gọi service rồi đổi kết quả hoặc lỗi thành response.
package httpapi

import (
	"fmt"
	"net/http"
	"net/netip"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/toidicode/taskflow/internal/attachment"
	"github.com/toidicode/taskflow/internal/auth"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/storage"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/user"
)

// Deps là những gì tầng HTTP cần, main tạo ra và truyền vào.
type Deps struct {
	Tasks       *task.Service
	Projects    *project.Service
	Users       user.Store
	Attachments attachment.Store // thông tin file đính kèm
	Files       storage.Storage  // nội dung file đính kèm
	Tokens      *auth.Tokens
	Ready       *Readiness // trả lời /ready
	RateLimit   RateLimit  // giới hạn số request, Limiter nil là tắt
}

// New tạo handler của cả API: một *gin.Engine (cũng là http.Handler) với các middleware dùng chung,
// theo thứ tự từ ngoài vào trong. trusted là các proxy tin cậy (VD Caddy) được phép báo IP client
// qua X-Forwarded-For.
func New(d Deps, corsOrigin string, trusted []netip.Prefix) http.Handler {
	r := newEngine(trusted)
	r.Use( // Use phải đứng trước khi đăng ký route: route đăng ký trước đó không có middleware này
		requestID,    // ngoài cùng: các middleware sau đều đọc được ID
		logRequests,  // log cả response 500 do recoverPanic trả về
		recoverPanic, // bắt panic của mọi thứ phía trong
		cors(corsOrigin),
	)
	routes(r, d)
	return r
}

// newEngine tạo engine Gin chưa có middleware nào (gin.New, không phải gin.Default:
// TaskFlow có sẵn middleware log và recover của mình) và chỉnh cho giống ServeMux.
func newEngine(trusted []netip.Prefix) *gin.Engine {
	r := gin.New()
	r.HandleMethodNotAllowed = true // sai method: 405 kèm header Allow, mặc định Gin trả 404
	r.RedirectTrailingSlash = false // /api/v1/tasks/ là 404 như ServeMux, không tự chuyển hướng

	// Mặc định Gin tin MỌI proxy (0.0.0.0/0, ::/0) và đọc cả X-Real-IP: client nào cũng tự khai được IP.
	// c.ClientIP() chỉ đọc X-Forwarded-For khi kết nối đến từ proxy tin cậy.
	r.RemoteIPHeaders = []string{"X-Forwarded-For"} // bỏ X-Real-IP
	proxies := make([]string, len(trusted))
	for i, p := range trusted {
		proxies[i] = p.String()
	}
	if err := r.SetTrustedProxies(proxies); err != nil { // rỗng: không tin proxy nào
		panic(err) // không xảy ra: trusted đã qua ParseTrustedProxies
	}
	return r
}

// routes đăng ký các route của API vào r. Route dưới /api/v1 cần access token,
// trừ đăng ký và đăng nhập.
func routes(r *gin.Engine, d Deps) {
	get(r, "/health", health)            // liveness: process còn sống, không hỏi database
	get(r, "/ready", gin.WrapH(d.Ready)) // readiness: http.Handler sẵn có, dùng lại qua gin.WrapH
	get(r, "/openapi.yaml", openAPISpec) // tài liệu API (api/openapi.yaml)
	get(r, "/docs", docs)                // Swagger UI đọc /openapi.yaml

	rl, lim := d.RateLimit, d.RateLimit.Limits
	v1 := r.Group("/api/v1")
	// đăng ký, đăng nhập: chặn theo IP trước khi đọc body và chạy bcrypt; Redis lỗi thì vẫn
	// giới hạn bằng bộ nhớ của từng bản API (không cho qua hết, cũng không chặn hết)
	v1.POST("/auth/register", rl.middleware(policy{"register-ip", lim.AuthIP, false}, ipKey), register(d.Users))
	v1.POST("/auth/login", rl.middleware(policy{"login-ip", lim.AuthIP, false}, ipKey), login(d.Users, d.Tokens, rl))

	// mọi route của nhóm api phải đăng nhập: requireAuth chạy trước handler của từng route,
	// rồi tới giới hạn theo user (Redis lỗi thì cho qua: luật này để chia đều tài nguyên, không để chống tấn công)
	api := v1.Group("", requireAuth(d.Tokens), rl.middleware(policy{"user", lim.User, true}, userKey))
	get(api, "/me", me(d.Users))

	projects := api.Group("/projects")
	get(projects, "", listProjects(d.Projects))
	projects.POST("", createProject(d.Projects))
	get(projects, "/:id", getProject(d.Projects))
	projects.PUT("/:id", replaceProject(d.Projects))
	projects.DELETE("/:id", deleteProject(d.Projects))
	get(projects, "/:id/tasks", listProjectTasks(d.Projects, d.Tasks))

	tasks := api.Group("/tasks")
	get(tasks, "", listTasks(d.Tasks))
	tasks.POST("", createTask(d.Tasks))
	get(tasks, "/:id", getTask(d.Tasks))
	tasks.PATCH("/:id", updateTask(d.Tasks))
	tasks.DELETE("/:id", deleteTask(d.Tasks))
	tasks.POST("/:id/tags", addTaskTag(d.Tasks))
	tasks.DELETE("/:id/tags/:name", removeTaskTag(d.Tasks))
	tasks.POST("/:id/attachments", uploadAttachment(d))
	get(tasks, "/:id/attachments", listAttachments(d))

	get(api, "/attachments/:id", downloadAttachment(d))
	api.DELETE("/attachments/:id", deleteAttachment(d))
}

// get đăng ký h cho cả GET và HEAD. ServeMux tự cho HEAD dùng route GET, Gin thì không.
func get(r gin.IRoutes, path string, h gin.HandlerFunc) {
	r.Match([]string{http.MethodGet, http.MethodHead}, path, h)
}

// health cho biết server còn sống, dùng cho Docker/load balancer kiểm tra.
func health(c *gin.Context) {
	c.String(http.StatusOK, "ok\n")
}

// pathID đọc tham số :id trên path và đổi sang số nguyên dương.
// id sai thì ghi response 400 và trả về false.
func pathID(c *gin.Context) (int, bool) {
	s := c.Param("id")
	id, err := strconv.Atoi(s)
	if err != nil || id < 1 {
		badParamsJSON(c, validator{"id": fmt.Sprintf("cần một số nguyên dương, nhận %q", s)})
		return 0, false
	}
	return id, true
}
