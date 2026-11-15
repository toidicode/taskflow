package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"time"

	"github.com/toidicode/taskflow/internal/attachment"
	"github.com/toidicode/taskflow/internal/storage"
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

// uploadAttachment nhận một file (multipart/form-data, field "file") và đính kèm vào task {id}.
// Body được đọc dạng stream, file được ghi thẳng vào storage, không giữ cả file trong bộ nhớ.
func uploadAttachment(s stores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		// kiểm tra quyền trước khi đọc body: người ngoài không đẩy được 10 MB nào vào server
		t, p, ok := loadTask(w, r, s, id)
		if !ok {
			return
		}
		me := userIDFrom(r.Context())
		if !attachment.CanUpload(me, p, t) {
			forbidden(w, "không được đính kèm file vào task này")
			return
		}
		if r.ContentLength > maxUploadBody { // client báo trước kích thước: từ chối khi chưa đọc byte nào
			tooLarge(w)
			return
		}
		extendDeadlines(w, r)
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadBody)

		mr, err := r.MultipartReader()
		if err != nil {
			errorJSON(w, http.StatusUnsupportedMediaType, "Content-Type phải là multipart/form-data")
			return
		}
		part, err := filePart(mr)
		if err != nil {
			badUpload(w, r, err)
			return
		}

		// loại file lấy từ 512 byte đầu của nội dung, không tin Content-Type hay đuôi file client gửi
		head := make([]byte, 512)
		n, err := io.ReadFull(part, head)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			badUpload(w, r, err)
			return
		}
		head = head[:n]
		if n == 0 {
			invalidJSON(w, validator{"file": "file rỗng"})
			return
		}
		contentType := http.DetectContentType(head)
		if !allowedTypes[contentType] {
			invalidJSON(w, validator{"file": fmt.Sprintf("không nhận file %s, chỉ nhận ảnh PNG, JPEG, GIF, WebP, PDF và text", contentType)})
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
		a.Size, err = s.files.Save(r.Context(), a.StoredName, readErrors{io.LimitReader(content, maxUploadBytes+1)})
		if err == nil && a.Size > maxUploadBytes {
			removeFiles(r, s.files, *a)
			err = readError{&http.MaxBytesError{Limit: maxUploadBytes}}
		}
		if _, ok := errors.AsType[readError](err); ok {
			badUpload(w, r, err)
			return
		}
		if err != nil {
			storeError(w, r, err)
			return
		}

		if err := s.attachments.Add(r.Context(), a); err != nil {
			removeFiles(r, s.files, *a) // không để lại file mà không hàng nào trỏ tới
			storeError(w, r, err)       // task vừa bị xóa: 404
			return
		}
		w.Header().Set("Location", fmt.Sprintf("/api/v1/attachments/%d", a.ID))
		writeJSON(w, http.StatusCreated, a)
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
func badUpload(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errNoFile):
		invalidJSON(w, validator{"file": err.Error()})
	case errors.As(err, new(*http.MaxBytesError)):
		tooLarge(w)
	case errors.Is(err, os.ErrDeadlineExceeded):
		errorJSON(w, http.StatusRequestTimeout, "gửi file quá lâu")
	default:
		requestLogger(r).Warn("body upload hỏng", "err", err)
		errorJSON(w, http.StatusBadRequest, "body multipart không hợp lệ")
	}
}

// tooLarge trả 413 khi file vượt quá maxUploadBytes.
func tooLarge(w http.ResponseWriter) {
	errorJSON(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file tối đa %d MB", maxUploadBytes>>20))
}

// extendDeadlines cho request này transferTimeout để nhận body và gửi response, thay cho
// ReadTimeout, WriteTimeout chung của server (10 giây, không đủ cho file lớn qua mạng chậm).
func extendDeadlines(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w) // tìm tới kết nối thật qua Unwrap của các middleware
	deadline := time.Now().Add(transferTimeout)
	for _, err := range []error{rc.SetReadDeadline(deadline), rc.SetWriteDeadline(deadline)} {
		if err != nil && !errors.Is(err, http.ErrNotSupported) { // httptest.ResponseRecorder không hỗ trợ
			requestLogger(r).Warn("không đặt được deadline", "err", err)
		}
	}
}

// listAttachments trả về các file đính kèm của task {id}.
func listAttachments(s stores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		if _, _, ok := loadTask(w, r, s, id); !ok {
			return
		}
		list, err := s.attachments.List(r.Context(), id)
		if err != nil {
			storeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
	}
}

// downloadAttachment gửi nội dung file {id} cho người xem được task chứa nó.
func downloadAttachment(s stores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		a, _, _, ok := loadAttachment(w, r, s, id)
		if !ok {
			return
		}
		f, err := s.files.Open(r.Context(), a.StoredName)
		if err != nil { // có hàng mà không có file: lỗi của server
			storeError(w, r, fmt.Errorf("mở file của attachment #%d: %w", id, err))
			return
		}
		defer f.Close()
		extendDeadlines(w, r)

		h := w.Header()
		h.Set("Content-Type", a.ContentType)                     // loại đã kiểm tra lúc upload
		h.Set("X-Content-Type-Options", "nosniff")               // trình duyệt không tự đoán ra loại khác
		h.Set("Content-Disposition", contentDisposition(a.Name)) // tải về, không mở ngay trong trình duyệt
		http.ServeContent(w, r, "", a.CreatedAt, f)
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

// deleteAttachment xóa file {id}: chủ project xóa được mọi file, người khác chỉ xóa file mình upload.
func deleteAttachment(s stores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		a, t, p, ok := loadAttachment(w, r, s, id)
		if !ok {
			return
		}
		if !attachment.CanDelete(userIDFrom(r.Context()), p, t, a) {
			forbidden(w, "chỉ chủ project hoặc người upload được xóa file")
			return
		}
		// xóa hàng trước, file sau: bước sau lỗi thì chỉ thừa một file không ai thấy;
		// làm ngược lại thì lỗi để lại một hàng trỏ tới file không còn
		if err := s.attachments.Delete(r.Context(), id); err != nil {
			storeError(w, r, err)
			return
		}
		removeFiles(r, s.files, a)
		w.WriteHeader(http.StatusNoContent)
	}
}

// removeFiles xóa nội dung các file đính kèm khỏi storage. Lỗi chỉ được ghi log: hàng trong
// database đã bị xóa (hoặc chưa từng có), file sót lại chỉ tốn chỗ, không ai tải được.
func removeFiles(r *http.Request, files storage.Storage, list ...attachment.Attachment) {
	ctx := context.WithoutCancel(r.Context()) // client ngắt kết nối thì vẫn phải dọn file
	for _, a := range list {
		if err := files.Remove(ctx, a.StoredName); err != nil {
			requestLogger(r).Error("xóa file đính kèm", "stored_name", a.StoredName, "err", err)
		}
	}
}
