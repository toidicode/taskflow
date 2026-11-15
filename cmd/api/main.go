// Command api là REST API của TaskFlow.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
)

func main() {
	addr := flag.String("addr", ":8080", "địa chỉ server lắng nghe, dạng host:port")
	flag.Parse()

	projects := project.NewMemoryStore()
	tasks := task.NewMemoryStore()
	if err := seed(projects, tasks); err != nil {
		log.Fatal(err)
	}

	srv := newServer(*addr, newMux(projects, tasks))
	log.Printf("TaskFlow API lắng nghe tại %s", *addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// newServer tạo http.Server có đủ timeout cho handler h.
func newServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,  // đọc xong header
		ReadTimeout:       10 * time.Second, // đọc xong cả request, gồm body
		WriteTimeout:      10 * time.Second, // ghi xong response
		IdleTimeout:       60 * time.Second, // chờ request kế tiếp trên kết nối keep-alive
	}
}

// seed thêm vài project và task mẫu. Từ Bài 29 dữ liệu sẽ nằm trong PostgreSQL.
func seed(projects *project.MemoryStore, tasks task.Store) error {
	projects.Add(project.New(1, "TaskFlow API", 1))
	projects.Add(project.New(2, "Website giới thiệu", 1))

	samples := []struct {
		projectID int
		title     string
		status    task.Status
	}{
		{1, "Thiết kế database", task.StatusDone},
		{1, "Viết API danh sách task", task.StatusDoing},
		{1, "Viết test cho API", task.StatusTodo},
		{2, "Viết tài liệu", task.StatusTodo},
	}
	for _, x := range samples {
		t, err := task.New(x.projectID, x.title)
		if err != nil {
			return err
		}
		switch x.status {
		case task.StatusDoing:
			t.Start()
		case task.StatusDone:
			t.Complete()
		}
		tasks.Add(t)
	}
	return nil
}
