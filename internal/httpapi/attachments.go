package httpapi

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/toidicode/taskflow/internal/attachment"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/storage"
	"github.com/toidicode/taskflow/internal/task"
)

const (
	maxUploadBytes  = 10 << 20               // mỗi file tối đa 10 MB
	maxUploadBody   = maxUploadBytes + 1<<20 // cả body: thêm 1 MB cho header, boundary của multipart
	transferTimeout = 5 * time.Minute        // thời gian tối đa để nhận hoặc gửi một file
)

// allowedTypes là các loại file được upload, so với kết quả của http.DetectContentType.
var allowedTypes = map[string]bool{
	"image/png":                 true,
	"image/jpeg":                true,
	"image/gif":                 true,
	"image/webp":                true,
	"application/pdf":           true,
	"text/plain; charset=utf-8": true,
}

// errNoFile: body không có part nào tên "file" kèm tên file.
var errNoFile = errors.New(`cần một file trong field "file"`)

// uploadAttachment nhận một file (multipart/form-data, field "file") và đính kèm vào task :id.
// Body được đọc dạng stream, file được ghi thẳng vào storage, không giữ cả file trong bộ nhớ:
// không dùng c.FormFile của Gin (nó gọi ParseMultipartForm, file lớn bị ghi ra file tạm trước).
func uploadAttachment(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		// kiểm tra quyền trước khi đọc body: người ngoài không đẩy được 10 MB nào vào server
		me := userIDFrom(c)
		t, p, err := d.Tasks.Get(c.Request.Context(), me, id)
		if err != nil {
			writeError(c, err)
			return
		}
		if !attachment.CanUpload(me, p, t) {
			forbidden(c, "không được đính kèm file vào task này")
			return
		}
		if c.Request.ContentLength > maxUploadBody { // client báo trước kích thước: từ chối khi chưa đọc byte nào
			tooLarge(c)
			return
		}
		extendDeadlines(c)
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBody)

		mr, err := c.Request.MultipartReader() // c.Request vẫn là *http.Request: đọc stream như Bài 35
		if err != nil {
			errorJSON(c, http.StatusUnsupportedMediaType, "Content-Type phải là multipart/form-data")
			return
		}
		part, err := filePart(mr)
		if err != nil {
			badUpload(c, err)
			return
		}

		// loại file lấy từ 512 byte đầu của nội dung, không tin Content-Type hay đuôi file client gửi
		head := make([]byte, 512)
		n, err := io.ReadFull(part, head)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			badUpload(c, err)
			return
		}
		head = head[:n]
		if n == 0 {
			invalidJSON(c, validator{"file": "file rỗng"})
			return
		}
		contentType := http.DetectContentType(head)
		if !allowedTypes[contentType] {
			invalidJSON(c, validator{"file": fmt.Sprintf("không nhận file %s, chỉ nhận ảnh PNG, JPEG, GIF, WebP, PDF và text", contentType)})
			return
		}

		a := &attachment.Attachment{
			TaskID:      t.ID,
			Name:        attachment.CleanName(part.FileName()), // chỉ là thông tin để hiển thị
			StoredName:  rand.Text(),                           // tên thật trên đĩa: 26 ký tự ngẫu nhiên
			ContentType: contentType,
			UploadedBy:  me,
		}
		content := io.MultiReader(bytes.NewReader(head), part) // ghép lại 512 byte đã đọc với phần còn lại
		a.Size, err = d.Files.Save(c.Request.Context(), a.StoredName, readErrors{io.LimitReader(content, maxUploadBytes+1)})
		if err == nil && a.Size > maxUploadBytes {
			removeFiles(c, d.Files, *a)
			err = readError{&http.MaxBytesError{Limit: maxUploadBytes}}
		}
		if _, ok := errors.AsType[readError](err); ok {
			badUpload(c, err)
			return
		}
		if err != nil {
			writeError(c, err)
			return
		}

		if err := d.Attachments.Add(c.Request.Context(), a); err != nil {
			removeFiles(c, d.Files, *a) // không để lại file mà không hàng nào trỏ tới
			writeError(c, err)          // task vừa bị xóa: 404
			return
		}
		c.Header("Location", fmt.Sprintf("/api/v1/attachments/%d", a.ID))
		writeJSON(c, http.StatusCreated, a)
	}
}

// filePart trả về part đầu tiên có tên "file" và có tên file, bỏ qua các part khác.
func filePart(mr *multipart.Reader) (*multipart.Part, error) {
	for {
		part, err := mr.NextPart() // NextPart tự đọc bỏ phần còn lại của part trước
		if errors.Is(err, io.EOF) {
			return nil, errNoFile
		}
		if err != nil {
			return nil, err
		}
		if part.FormName() == "file" && part.FileName() != "" {
			return part, nil
		}
	}
}

// readError đánh dấu lỗi xảy ra khi đọc body của request (lỗi của client: 400, 413),
// để phân biệt với lỗi khi ghi vào storage (lỗi của server: 500).
type readError struct{ err error }

func (e readError) Error() string { return e.err.Error() }
func (e readError) Unwrap() error { return e.err }

// readErrors bọc mọi lỗi đọc (trừ io.EOF) của r trong readError.
type readErrors struct{ r io.Reader }

func (rr readErrors) Read(p []byte) (int, error) {
	n, err := rr.r.Read(p)
	if err != nil && err != io.EOF {
		err = readError{err}
	}
	return n, err
}

// badUpload trả lỗi khi đọc body upload: 422 khi thiếu file, 413 khi vượt giới hạn,
// 408 khi client gửi quá chậm, còn lại 400. Chi tiết lỗi chỉ ghi vào log.
func badUpload(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errNoFile):
		invalidJSON(c, validator{"file": err.Error()})
	case errors.As(err, new(*http.MaxBytesError)):
		tooLarge(c)
	case errors.Is(err, os.ErrDeadlineExceeded):
		errorJSON(c, http.StatusRequestTimeout, "gửi file quá lâu")
	default:
		requestLogger(c).Warn("body upload hỏng", "err", err)
		errorJSON(c, http.StatusBadRequest, "body multipart không hợp lệ")
	}
}

// tooLarge trả 413 khi file vượt quá maxUploadBytes.
func tooLarge(c *gin.Context) {
	errorJSON(c, http.StatusRequestEntityTooLarge, fmt.Sprintf("file tối đa %d MB", maxUploadBytes>>20))
}

// extendDeadlines cho request này transferTimeout để nhận body và gửi response, thay cho
// ReadTimeout, WriteTimeout chung của server (10 giây, không đủ cho file lớn qua mạng chậm).
func extendDeadlines(c *gin.Context) {
	rc := http.NewResponseController(c.Writer) // tìm tới kết nối thật qua Unwrap() của c.Writer
	deadline := time.Now().Add(transferTimeout)
	for _, err := range []error{rc.SetReadDeadline(deadline), rc.SetWriteDeadline(deadline)} {
		if err != nil && !errors.Is(err, http.ErrNotSupported) { // httptest.ResponseRecorder không hỗ trợ
			requestLogger(c).Warn("không đặt được deadline", "err", err)
		}
	}
}

// listAttachments trả về các file đính kèm của task :id.
func listAttachments(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		if _, _, err := d.Tasks.Get(c.Request.Context(), userIDFrom(c), id); err != nil {
			writeError(c, err)
			return
		}
		list, err := d.Attachments.List(c.Request.Context(), id)
		if err != nil {
			writeError(c, err)
			return
		}
		writeJSON(c, http.StatusOK, list)
	}
}

// downloadAttachment gửi nội dung file :id cho người xem được task chứa nó.
func downloadAttachment(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		a, _, _, ok := loadAttachment(c, d, id)
		if !ok {
			return
		}
		f, err := d.Files.Open(c.Request.Context(), a.StoredName)
		if err != nil { // có hàng mà không có file: lỗi của server
			writeError(c, fmt.Errorf("mở file của attachment #%d: %w", id, err))
			return
		}
		defer f.Close()
		extendDeadlines(c)

		h := c.Writer.Header()
		h.Set("Content-Type", a.ContentType)                     // loại đã kiểm tra lúc upload
		h.Set("X-Content-Type-Options", "nosniff")               // trình duyệt không tự đoán ra loại khác
		h.Set("Content-Disposition", contentDisposition(a.Name)) // tải về, không mở ngay trong trình duyệt
		http.ServeContent(c.Writer, c.Request, "", a.CreatedAt, f)
	}
}

// contentDisposition tạo header Content-Disposition: attachment với tên file name.
// Tên có dấu được mã hóa theo RFC 2231 (tham số filename* thay cho filename).
func contentDisposition(name string) string {
	if v := mime.FormatMediaType("attachment", map[string]string{"filename": name}); v != "" {
		return v
	}
	return "attachment"
}

// deleteAttachment xóa file :id: chủ project xóa được mọi file, người khác chỉ xóa file mình upload.
func deleteAttachment(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		a, t, p, ok := loadAttachment(c, d, id)
		if !ok {
			return
		}
		if !attachment.CanDelete(userIDFrom(c), p, t, a) {
			forbidden(c, "chỉ chủ project hoặc người upload được xóa file")
			return
		}
		// xóa hàng trước, file sau: bước sau lỗi thì chỉ thừa một file không ai thấy;
		// làm ngược lại thì lỗi để lại một hàng trỏ tới file không còn
		if err := d.Attachments.Delete(c.Request.Context(), id); err != nil {
			writeError(c, err)
			return
		}
		removeFiles(c, d.Files, a)
		c.Status(http.StatusNoContent)
	}
}

// removeFiles xóa nội dung các file đính kèm khỏi storage, lỗi chỉ được ghi log (attachment.Files.Remove).
func removeFiles(c *gin.Context, files storage.Storage, list ...attachment.Attachment) {
	names := make([]string, len(list))
	for i, a := range list {
		names[i] = a.StoredName
	}
	attachment.Files{Storage: files}.Remove(c.Request.Context(), names...)
}

// loadAttachment đọc file đính kèm id cùng task và project chứa nó. User không được xem task
// thì ghi response 404 giống hệt khi không có file (không lộ cả ID của task) rồi trả về false.
func loadAttachment(c *gin.Context, d Deps, id int) (attachment.Attachment, task.Task, project.Project, bool) {
	a, err := d.Attachments.Get(c.Request.Context(), id)
	if err != nil {
		writeError(c, err)
		return attachment.Attachment{}, task.Task{}, project.Project{}, false
	}
	t, p, err := d.Tasks.Get(c.Request.Context(), userIDFrom(c), a.TaskID)
	if errors.Is(err, task.ErrNotFound) {
		err = fmt.Errorf("attachment #%d: %w", id, attachment.ErrNotFound)
	}
	if err != nil {
		writeError(c, err)
		return attachment.Attachment{}, task.Task{}, project.Project{}, false
	}
	return a, t, p, true
}
