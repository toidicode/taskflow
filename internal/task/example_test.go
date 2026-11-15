package task_test

import (
	"fmt"

	"github.com/toidicode/taskflow/internal/task"
)

func ExampleNew() {
	t, err := task.New(1, "  Thiết kế database ")
	fmt.Printf("%q %v %v\n", t.Title, t.Status, err)

	_, err = task.New(0, "")
	fmt.Println(err)
	// Output:
	// "Thiết kế database" todo <nil>
	// project_id: phải lớn hơn 0
	// title: không được để trống
}
