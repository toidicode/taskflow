// Command taskflow là ứng dụng quản lý công việc mà chúng ta xây dựng trong series.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
)

// dataFile là file JSON lưu task giữa các lần chạy.
const dataFile = "tasks.json"

func main() {
	fmt.Println("TaskFlow", version)

	p := project.New(1, " Website bán hàng ", 1)
	p.Description = "Làm lại trang chủ và giỏ hàng"

	// lấy thời điểm hiện tại một lần, dùng chung cho cả chương trình
	now := time.Now()

	store := task.NewMemoryStore()
	if err := loadOrSeed(store, p.ID, now); err != nil {
		fmt.Println("Lỗi:", err)
		os.Exit(1)
	}

	// tìm task theo id, phân biệt "không có task" với các lỗi khác
	for _, id := range []int{3, 9} {
		t, err := store.Get(id)
		switch {
		case errors.Is(err, task.ErrNotFound):
			fmt.Println("Bỏ qua:", err)
		case err != nil:
			fmt.Println("Lỗi:", err)
		default:
			t.Assign(1)
		}
	}

	printReport(store, p, now)

	if err := store.Save(dataFile); err != nil {
		fmt.Println("Lỗi:", err)
		os.Exit(1)
	}
	fmt.Println("Đã lưu vào", dataFile)
}

// loadOrSeed đọc task từ dataFile, lần chạy đầu chưa có file thì tạo task mẫu.
func loadOrSeed(store *task.MemoryStore, projectID int, now time.Time) error {
	err := store.Load(dataFile)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Println("Chưa có", dataFile+", tạo task mẫu")
		return seedTasks(store, projectID, now)
	}
	if err != nil {
		return err
	}
	fmt.Printf("Đã đọc %d task từ %s\n", len(store.List()), dataFile)
	return nil
}

// seedTasks thêm vài task mẫu của project projectID vào store, hạn tính từ now.
func seedTasks(store task.Store, projectID int, now time.Time) error {
	titles := []string{"  Thiết kế database", "Viết API giỏ hàng", "Viết tài liệu"}
	tasks := make([]*task.Task, 0, len(titles))
	for _, title := range titles {
		t, err := task.New(projectID, title)
		if err != nil {
			return fmt.Errorf("tạo task mẫu: %w", err)
		}
		store.Add(t)
		tasks = append(tasks, t)
	}

	design, api := tasks[0], tasks[1]
	design.DueDate = now.AddDate(0, 0, -2) // hạn 2 ngày trước, nhưng đã xong
	design.Start()
	design.Complete()
	api.DueDate = now.AddDate(0, 0, -1) // hạn hôm qua, vẫn đang làm
	api.Assign(2)
	api.Start()
	// task thứ ba không có hạn: DueDate giữ zero value
	return nil
}

// printReport in các task trong store của project p, quá hạn tính tại thời điểm now.
func printReport(store task.Store, p *project.Project, now time.Time) {
	fmt.Printf("Project #%d %q của user #%d\n", p.ID, p.Name, p.OwnerID)
	total := 0
	for t := range store.All() {
		total++
		fmt.Printf("  #%d %-18s %-5s user #%d  %s\n", t.ID, t.Title, t.Status, t.AssigneeID, dueText(t, now))
	}

	done := slices.Collect(filter(store.All(), func(t *task.Task) bool { return t.IsDone() }))
	overdue := slices.Collect(filter(store.All(), func(t *task.Task) bool { return t.IsOverdue(now) }))
	fmt.Printf("Đã xong %d/%d task, quá hạn %d task\n", len(done), total, len(overdue))
	if len(overdue) > 0 {
		titles := mapSeq(slices.Values(overdue), func(t *task.Task) string { return t.Title })
		fmt.Println("Cần xử lý gấp:", strings.Join(slices.Collect(titles), ", "))
	}
}

// filter trả về iterator gồm các phần tử của seq mà keep trả về true.
func filter[T any](seq iter.Seq[T], keep func(T) bool) iter.Seq[T] {
	return func(yield func(T) bool) {
		for v := range seq {
			if keep(v) && !yield(v) {
				return
			}
		}
	}
}

// mapSeq trả về iterator gồm kết quả của f với từng phần tử của seq.
func mapSeq[T, U any](seq iter.Seq[T], f func(T) U) iter.Seq[U] {
	return func(yield func(U) bool) {
		for v := range seq {
			if !yield(f(v)) {
				return
			}
		}
	}
}

// dueText mô tả hạn của task t, VD "hạn 14/11/2026 (quá hạn)".
func dueText(t *task.Task, now time.Time) string {
	if t.DueDate.IsZero() {
		return "không có hạn"
	}
	s := "hạn " + t.DueDate.Format("02/01/2006")
	if t.IsOverdue(now) {
		s += " (quá hạn)"
	}
	return s
}
