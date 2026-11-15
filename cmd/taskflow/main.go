// Command taskflow là ứng dụng quản lý công việc mà chúng ta xây dựng trong series.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// dataFile là file JSON lưu task giữa các lần chạy.
const dataFile = "tasks.json"

func main() {
	fmt.Println("TaskFlow", version)

	p := NewProject(1, " Website bán hàng ", 1)
	p.Description = "Làm lại trang chủ và giỏ hàng"

	store := NewMemoryTaskStore()
	if err := loadOrSeed(store, p.ID); err != nil {
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

	printReport(store, p)

	if err := store.Save(dataFile); err != nil {
		fmt.Println("Lỗi:", err)
		os.Exit(1)
	}
	fmt.Println("Đã lưu vào", dataFile)
}

// loadOrSeed đọc task từ dataFile, lần chạy đầu chưa có file thì tạo task mẫu.
func loadOrSeed(store *MemoryTaskStore, projectID int) error {
	err := store.Load(dataFile)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Println("Chưa có", dataFile+", tạo task mẫu")
		return seedTasks(store, projectID)
	}
	if err != nil {
		return err
	}
	fmt.Printf("Đã đọc %d task từ %s\n", len(store.List()), dataFile)
	return nil
}

// seedTasks thêm vài task mẫu của project projectID vào store.
func seedTasks(store TaskStore, projectID int) error {
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
	design.Start()
	design.Complete()
	api.Assign(2)
	api.Start()
	return nil
}

// printReport in các task trong store của project p.
func printReport(store TaskStore, p *Project) {
	tasks := store.List()
	fmt.Printf("Project #%d %q của user #%d\n", p.ID, p.Name, p.OwnerID)
	done := 0
	for _, t := range tasks {
		fmt.Printf("  #%d %-18s %-5s user #%d\n", t.ID, t.Title, t.Status, t.AssigneeID)
		if t.IsDone() {
			done++
		}
	}
	fmt.Printf("Đã xong %d/%d task\n", done, len(tasks))
}
