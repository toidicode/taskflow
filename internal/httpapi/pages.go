package httpapi

import (
	"bytes"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/toidicode/taskflow/internal/task"
)

// webFS chứa template và file tĩnh của trang web, nhúng vào binary lúc build.
//
//go:embed web
var webFS embed.FS

// funcs là các hàm template gọi được, phải khai báo trước khi Parse.
var funcs = template.FuncMap{
	"statuses": func() []task.Status { return []task.Status{task.StatusTodo, task.StatusDoing, task.StatusDone} },
	"statusLabel": func(s task.Status) string {
		switch s {
		case task.StatusDoing:
			return "Đang làm"
		case task.StatusDone:
			return "Xong"
		default:
			return "Cần làm"
		}
	},
}

// pages render các trang HTML của dashboard.
type pages struct {
	fsys   fs.FS
	reload bool      // dev: parse lại mỗi lần render, sửa file HTML rồi F5 là thấy ngay
	parsed templates // parse một lần lúc khởi động (khi không reload)
}

// templates: base gồm layout và partial; byName là mỗi trang (layout + partial + trang đó).
type templates struct {
	base   *template.Template
	byName map[string]*template.Template
}

// newPages parse mọi template một lần. dir rỗng: dùng template nhúng trong binary;
// dir khác rỗng (chỉ khi dev): đọc từ thư mục đó và parse lại mỗi request.
func newPages(dir string) (*pages, error) {
	p := &pages{reload: dir != ""}
	if p.reload {
		p.fsys = os.DirFS(dir)
	} else {
		p.fsys, _ = fs.Sub(webFS, "web") // "web" chắc chắn có: go:embed đã kiểm tra lúc build
	}
	var err error
	p.parsed, err = parseTemplates(p.fsys)
	return p, err
}

// parseTemplates đọc layout, partial, rồi parse mỗi trang trên một bản Clone của chúng: trang
// nào cũng define "title" và "content", parse chung một tập thì trang sau đè lên trang trước.
func parseTemplates(fsys fs.FS) (templates, error) {
	base, err := template.New("").Funcs(funcs).ParseFS(fsys, "layout.html", "partials/*.html")
	if err != nil {
		return templates{}, err
	}
	files, err := fs.Glob(fsys, "pages/*.html")
	if err != nil {
		return templates{}, err
	}
	byName := make(map[string]*template.Template, len(files))
	for _, file := range files {
		t, err := base.Clone() // Clone được khi base chưa Execute lần nào
		if err != nil {
			return templates{}, err
		}
		if t, err = t.ParseFS(fsys, file); err != nil {
			return templates{}, err
		}
		byName[strings.TrimSuffix(path.Base(file), ".html")] = t
	}
	return templates{base: base, byName: byName}, nil
}

// page render trang name (pages/name.html) trong layout.
func (p *pages) page(c *gin.Context, status int, name string, v view) {
	p.execute(c, status, func(ts templates) *template.Template { return ts.byName[name] }, "layout", v)
}

// partial render riêng một template trong partials/, VD một dòng task cho htmx.
func (p *pages) partial(c *gin.Context, status int, name string, data any) {
	p.execute(c, status, func(ts templates) *template.Template { return ts.base }, name, data)
}

// execute render vào buffer trước rồi mới ghi response: template lỗi giữa chừng thì
// client nhận 500, không phải 200 kèm nửa trang.
func (p *pages) execute(c *gin.Context, status int, pick func(templates) *template.Template, name string, data any) {
	ts := p.parsed
	if p.reload {
		var err error
		if ts, err = parseTemplates(p.fsys); err != nil {
			requestLogger(c).Error("parse template", "err", err)
			c.String(http.StatusInternalServerError, "lỗi template: %v", err)
			return
		}
	}
	var buf bytes.Buffer
	t := pick(ts)
	if t == nil {
		requestLogger(c).Error("không có template", "name", name)
		c.String(http.StatusInternalServerError, "lỗi máy chủ")
		return
	}
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		requestLogger(c).Error("render template", "err", err)
		c.String(http.StatusInternalServerError, "lỗi máy chủ")
		return
	}
	c.Data(status, "text/html; charset=utf-8", buf.Bytes())
}
