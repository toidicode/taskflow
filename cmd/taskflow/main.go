// Command taskflow là ứng dụng quản lý công việc mà chúng ta xây dựng trong series.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// dataFile là file JSON lưu task giữa các lần chạy.
const dataFile = "tasks.json"

func main() {
	fmt.Println("TaskFlow", version)

	p := NewProject(1, " Website bán hàng ", 1)
	p.Description = "Làm lại trang chủ và giỏ hàng"

	// lấy thời điểm hiện tại một lần, dùng chung cho cả chương trình
	now := time.Now()

	store := NewMemoryTaskStore()
	if err := loadOrSeed(store, p.ID, now); err != nil {
		fmt.Println("Lỗi:", err)
		os.Exit(1)
	}

	// tìm task theo id, phân biệt "không có task" với các lỗi khác
	for _, id := range []int{3, 9} {
		t, err := store.Get(id)
		switch {
		case errors.Is(err, ErrNotFound):
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
func loadOrSeed(store *MemoryTaskStore, projectID int, now time.Time) error {
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
func seedTasks(store TaskStore, projectID int, now time.Time) error {
	titles := []string{"  Thiết kế database", "Viết API giỏ hàng", "Viết tài liệu"}
	tasks := make([]*Task, 0, len(titles))
	for _, title := range titles {
		t, err := NewTask(projectID, title)
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
func printReport(store TaskStore, p *Project, now time.Time) {
	tasks := store.List()
	fmt.Printf("Project #%d %q của user #%d\n", p.ID, p.Name, p.OwnerID)
	done, overdue := 0, 0
	for _, t := range tasks {
		fmt.Printf("  #%d %-18s %-5s user #%d  %s\n", t.ID, t.Title, t.Status, t.AssigneeID, dueText(t, now))
		if t.IsDone() {
			done++
		}
		if t.IsOverdue(now) {
			overdue++
		}
	}
	fmt.Printf("Đã xong %d/%d task, quá hạn %d task\n", done, len(tasks), overdue)
}

// dueText mô tả hạn của task t, VD "hạn 14/11/2026 (quá hạn)".
func dueText(t *Task, now time.Time) string {
	if t.DueDate.IsZero() {
		return "không có hạn"
	}
	s := "hạn " + t.DueDate.Format("02/01/2006")
	if t.IsOverdue(now) {
		s += " (quá hạn)"
	}
	return s
}
