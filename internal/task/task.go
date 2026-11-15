// Package task chứa model Task và nơi lưu trữ task của TaskFlow.
package task

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Status là trạng thái của một task.
type Status int

const (
	StatusTodo Status = iota
	StatusDoing
	StatusDone
)

// String trả về tên của trạng thái, fmt dùng nó khi in một Status.
func (s Status) String() string {
	switch s {
	case StatusTodo:
		return "todo"
	case StatusDoing:
		return "doing"
	case StatusDone:
		return "done"
	}
	return "Status(" + strconv.Itoa(int(s)) + ")"
}

// ParseStatus chuyển tên trạng thái ("todo", "doing", "done") thành Status.
func ParseStatus(name string) (Status, error) {
	for _, s := range []Status{StatusTodo, StatusDoing, StatusDone} {
		if s.String() == name {
			return s, nil
		}
	}
	return 0, fmt.Errorf("trạng thái %q không hợp lệ, chọn todo, doing hoặc done", name)
}

// MarshalText cho encoding/json (và các package khác) ghi Status thành "todo", "doing", "done".
func (s Status) MarshalText() ([]byte, error) {
	switch s {
	case StatusTodo, StatusDoing, StatusDone:
		return []byte(s.String()), nil
	}
	return nil, fmt.Errorf("trạng thái %d không hợp lệ", int(s))
}

// UnmarshalText đọc Status từ tên trạng thái, receiver là con trỏ vì phải sửa s.
func (s *Status) UnmarshalText(text []byte) error {
	status, err := ParseStatus(string(text))
	if err != nil {
		return err
	}
	*s = status
	return nil
}

// Task là một công việc thuộc một project.
type Task struct {
	ID          int       `json:"id"`
	ProjectID   int       `json:"project_id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Status      Status    `json:"status"`
	AssigneeID  int       `json:"assignee_id,omitempty"` // 0 nghĩa là chưa giao cho ai
	DueDate     time.Time `json:"due_date,omitzero"`     // zero value nghĩa là không có hạn
	CompletedAt time.Time `json:"completed_at,omitzero"` // zero value nghĩa là chưa xong
	Tags        []string  `json:"tags,omitempty"`        // tên tag, đã chuẩn hóa bằng NormalizeTags
}

// ValidationError cho biết một field có giá trị không hợp lệ.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

// Giới hạn độ dài (tính theo ký tự) của title, description, tên tag và số tag của một task.
const (
	MaxTitleLen       = 200
	MaxDescriptionLen = 2000
	MaxTagLen         = 30
	MaxTags           = 10
)

// NormalizeTag bỏ khoảng trắng hai đầu và đổi tên tag sang chữ thường: "Backend " và "backend" là một tag.
func NormalizeTag(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// NormalizeTags chuẩn hóa từng tên tag, bỏ tên trùng và sắp xếp theo thứ tự chữ cái.
func NormalizeTags(names []string) []string {
	tags := make([]string, 0, len(names))
	for _, name := range names {
		tags = append(tags, NormalizeTag(name))
	}
	slices.Sort(tags)
	return slices.Compact(tags)
}

// CheckTag trả về thông báo lỗi khi tên tag (đã chuẩn hóa) không hợp lệ, chuỗi rỗng nếu hợp lệ.
func CheckTag(name string) string {
	switch {
	case name == "":
		return "tên tag không được để trống"
	case utf8.RuneCountInString(name) > MaxTagLen:
		return fmt.Sprintf("tên tag tối đa %d ký tự", MaxTagLen)
	}
	return ""
}

// New tạo một task mới ở trạng thái todo, ID do Store cấp khi thêm task.
// Dữ liệu không hợp lệ thì trả về lỗi của Validate.
func New(projectID int, title string) (*Task, error) {
	t := &Task{
		ProjectID: projectID,
		Title:     strings.TrimSpace(title),
		Status:    StatusTodo,
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	return t, nil
}

// Validate kiểm tra các quy tắc mà task nào cũng phải thỏa, dù được tạo từ API, CLI hay file.
// Lỗi trả về chỉ gồm các *ValidationError gộp bằng errors.Join, nil nếu task hợp lệ.
func (t *Task) Validate() error {
	var errs []error
	check := func(ok bool, field, message string) {
		if !ok {
			errs = append(errs, &ValidationError{Field: field, Message: message})
		}
	}
	title := strings.TrimSpace(t.Title)
	check(t.ProjectID > 0, "project_id", "phải lớn hơn 0")
	check(title != "", "title", "không được để trống")
	check(utf8.RuneCountInString(title) <= MaxTitleLen, "title", fmt.Sprintf("tối đa %d ký tự", MaxTitleLen))
	check(utf8.RuneCountInString(t.Description) <= MaxDescriptionLen, "description",
		fmt.Sprintf("tối đa %d ký tự", MaxDescriptionLen))
	check(t.AssigneeID >= 0, "assignee_id", "không được âm")
	check(len(t.Tags) <= MaxTags, "tags", fmt.Sprintf("tối đa %d tag", MaxTags))
	for _, tag := range t.Tags {
		msg := CheckTag(tag)
		check(msg == "", "tags", msg)
	}
	return errors.Join(errs...)
}

// Assign giao task cho người dùng có id là userID.
func (t *Task) Assign(userID int) {
	t.AssigneeID = userID
}

// Start chuyển task sang trạng thái doing.
func (t *Task) Start() {
	t.Status = StatusDoing
}

// Complete đánh dấu task đã xong và ghi lại thời điểm hoàn thành.
func (t *Task) Complete() {
	t.Status = StatusDone
	t.CompletedAt = time.Now()
}

// SetStatus chuyển task sang trạng thái s: sang done thì ghi thời điểm hoàn thành
// (nếu chưa xong), sang todo/doing thì xóa thời điểm đó.
func (t *Task) SetStatus(s Status) {
	switch {
	case s == StatusDone && !t.IsDone():
		t.Complete()
	case s != StatusDone:
		t.Status = s
		t.CompletedAt = time.Time{}
	}
}

// HasTag cho biết task có tag name (đã chuẩn hóa) hay không.
func (t *Task) HasTag(name string) bool {
	return slices.Contains(t.Tags, name)
}

// IsDone cho biết task đã xong hay chưa.
func (t *Task) IsDone() bool {
	return t.Status == StatusDone
}

// IsOverdue cho biết tại thời điểm now task đã quá hạn mà chưa xong hay chưa.
func (t *Task) IsOverdue(now time.Time) bool {
	return !t.IsDone() && !t.DueDate.IsZero() && now.After(t.DueDate)
}
