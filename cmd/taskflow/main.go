// Command taskflow là ứng dụng quản lý công việc mà chúng ta xây dựng trong series.
package main

import "fmt"

func main() {
	fmt.Println("TaskFlow", version)

	p := NewProject(1, " Website bán hàng ", 1)
	p.Description = "Làm lại trang chủ và giỏ hàng"

	store := NewMemoryTaskStore()
	seedTasks(store, p.ID)

	// tìm task theo id, ok bằng false nghĩa là không có task đó
	for _, id := range []int{3, 9} {
		t, ok := store.Get(id)
		if !ok {
			fmt.Printf("Không có task #%d\n", id)
			continue
		}
		t.Assign(1)
	}

	printReport(store, p)
}

// seedTasks thêm vài task mẫu của project projectID vào store.
func seedTasks(store TaskStore, projectID int) {
	design := NewTask(projectID, "  Thiết kế database")
	api := NewTask(projectID, "Viết API giỏ hàng")
	store.Add(design)
	store.Add(api)
	store.Add(NewTask(projectID, "Viết tài liệu"))

	design.Start()
	design.Complete()
	api.Assign(2)
	api.Start()
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
