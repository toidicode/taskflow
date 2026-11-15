// Command taskflow là ứng dụng quản lý công việc mà chúng ta xây dựng trong series.
package main

import "fmt"

func main() {
	fmt.Println("TaskFlow", version)

	p := NewProject(1, " Website bán hàng ", 1)
	p.Description = "Làm lại trang chủ và giỏ hàng"

	// slice chứa con trỏ: mọi thay đổi đều áp dụng lên task gốc
	tasks := []*Task{
		NewTask(1, p.ID, "  Thiết kế database"),
		NewTask(2, p.ID, "Viết API giỏ hàng"),
		NewTask(3, p.ID, "Viết tài liệu"),
	}
	tasks[0].Start()
	tasks[0].Complete()
	tasks[1].Assign(2)
	tasks[1].Start()

	// tìm task theo id, nil nghĩa là không có task đó
	for _, id := range []int{3, 9} {
		t := FindTask(tasks, id)
		if t == nil {
			fmt.Printf("Không có task #%d\n", id)
			continue
		}
		t.Assign(1)
	}

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
