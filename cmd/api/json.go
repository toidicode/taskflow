package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"strings"
)

// maxBodyBytes giới hạn kích thước body JSON của request: 1 MB.
const maxBodyBytes = 1 << 20

// errUnsupportedMediaType được trả về khi request không gửi Content-Type: application/json.
var errUnsupportedMediaType = errors.New("Content-Type phải là application/json")

// writeJSON mã hóa v thành JSON rồi ghi ra response với status code cho trước.
func writeJSON(w http.ResponseWriter, status int, v any) {
	// mã hóa trước khi ghi: lỗi thì vẫn kịp đổi sang 500 vì chưa gửi header nào
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("writeJSON: %v", err)
		status, data = http.StatusInternalServerError, []byte(`{"error":"lỗi máy chủ"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(append(data, '\n'))
}

// errorResponse là định dạng lỗi chung của API. Fields chỉ có ở lỗi validate:
// {"error": "dữ liệu không hợp lệ", "fields": {"title": "không được để trống"}}.
type errorResponse struct {
	Error  string            `json:"error"`
	Fields map[string]string `json:"fields,omitempty"`
}

// errorJSON trả lỗi không gắn với field nào: {"error": "..."}.
func errorJSON(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

// readJSON đọc body của r (một JSON object) vào dst, trả lỗi có thông báo dễ hiểu cho client.
func readJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "application/json" {
		return errUnsupportedMediaType
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			return err // bodyError trả 413 cho lỗi này
		case errors.Is(err, io.EOF):
			return errors.New("body rỗng, cần một JSON object")
		case errors.As(err, &syntaxErr):
			return fmt.Errorf("JSON sai cú pháp ở byte thứ %d", syntaxErr.Offset)
		case errors.Is(err, io.ErrUnexpectedEOF):
			return errors.New("JSON bị cắt cụt, thiếu phần kết thúc")
		case errors.As(err, &typeErr) && typeErr.Field != "":
			return fmt.Errorf("field %q sai kiểu, cần %v", typeErr.Field, typeErr.Type)
		case errors.As(err, &typeErr):
			return errors.New("body phải là một JSON object")
		case strings.HasPrefix(err.Error(), "json: unknown field "):
			return fmt.Errorf("không hỗ trợ field %s", strings.TrimPrefix(err.Error(), "json: unknown field "))
		default:
			return err // VD lỗi từ UnmarshalText của task.Status
		}
	}

	// body chỉ được chứa đúng một giá trị JSON
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("body chỉ được chứa một JSON object")
	}
	return nil
}

// bodyError trả lỗi của readJSON với status code phù hợp: 415, 413 hoặc 400.
func bodyError(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	switch {
	case errors.Is(err, errUnsupportedMediaType):
		errorJSON(w, http.StatusUnsupportedMediaType, err.Error())
	case errors.As(err, &maxErr):
		errorJSON(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("body vượt quá %d byte", maxErr.Limit))
	default:
		errorJSON(w, http.StatusBadRequest, err.Error())
	}
}
