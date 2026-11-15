package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/toidicode/taskflow/internal/task"
)

// newFlagSet tạo FlagSet cho lệnh con name, synopsis là phần tham số in trong cách dùng.
func (a *app) newFlagSet(name, synopsis string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	flags.Usage = func() {
		fmt.Fprintf(a.stderr, "Cách dùng: taskflow %s %s\n", name, synopsis)
		flags.PrintDefaults()
	}
	return flags
}

// usagef in thông báo lỗi kèm cách dùng của lệnh ra stderr rồi trả về errUsage.
func usagef(flags *flag.FlagSet, format string, args ...any) error {
	fmt.Fprintf(flags.Output(), "taskflow %s: %s\n", flags.Name(), fmt.Sprintf(format, args...))
	flags.Usage()
	return errUsage
}

// idArg đọc id của task: đúng một tham số còn lại sau các flag.
func idArg(flags *flag.FlagSet) (int, error) {
	if flags.NArg() != 1 {
		return 0, usagef(flags, "cần đúng 1 id, nhận %d tham số %q", flags.NArg(), flags.Args())
	}
	id, err := strconv.Atoi(flags.Arg(0))
	if err != nil || id <= 0 {
		return 0, usagef(flags, "id %q không hợp lệ", flags.Arg(0))
	}
	return id, nil
}

// parseDue đọc ngày dạng YYYY-MM-DD theo múi giờ của máy, hạn là cuối ngày đó.
func parseDue(s string) (time.Time, error) {
	d, err := time.ParseInLocation(time.DateOnly, s, time.Local)
	if err != nil {
		return time.Time{}, errors.New("cần dạng YYYY-MM-DD")
	}
	return d.AddDate(0, 0, 1).Add(-time.Second), nil // 23:59:59 của ngày d
}

// defaultFile trả về file dữ liệu mặc định: biến môi trường TASKFLOW_FILE nếu có, không thì tasks.json.
func defaultFile() string {
	if f := os.Getenv("TASKFLOW_FILE"); f != "" {
		return f
	}
	return "tasks.json"
}

// loadStore đọc task từ file, file chưa có thì trả về store rỗng.
func loadStore(file string) (*task.MemoryStore, error) {
	s := task.NewMemoryStore()
	if err := s.Load(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return s, nil
}

// update đọc store từ file, gọi change, rồi lưu lại nếu change không lỗi.
func update(file string, change func(s *task.MemoryStore) error) error {
	s, err := loadStore(file)
	if err != nil {
		return err
	}
	if err := change(s); err != nil {
		return err
	}
	return s.Save(file)
}

// add: taskflow add -project N -title TEXT [-due YYYY-MM-DD]
func (a *app) add(file string, args []string) error {
	flags := a.newFlagSet("add", "-project N -title TEXT [-due YYYY-MM-DD]")
	projectID := flags.Int("project", 0, "id của project (bắt buộc)")
	title := flags.String("title", "", "tên task (bắt buộc)")
	var due time.Time
	flags.Func("due", "hạn hoàn thành, dạng YYYY-MM-DD", func(s string) error {
		var err error
		due, err = parseDue(s)
		return err
	})
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return usagef(flags, "thừa tham số %q", flags.Args())
	}

	t, err := task.New(*projectID, *title)
	if err != nil {
		return err
	}
	t.DueDate = due
	err = update(file, func(s *task.MemoryStore) error {
		s.Add(t)
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "Đã thêm task #%d: %s\n", t.ID, t.Title)
	return nil
}

// importTasks: taskflow import -project N, đọc stdin, mỗi dòng là title của một task.
func (a *app) importTasks(file string, args []string) error {
	flags := a.newFlagSet("import", "-project N < FILE")
	projectID := flags.Int("project", 0, "id của project (bắt buộc)")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return usagef(flags, "thừa tham số %q", flags.Args())
	}

	var tasks []*task.Task
	sc := bufio.NewScanner(a.stdin)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue // bỏ dòng trống và dòng ghi chú
		}
		t, err := task.New(*projectID, line)
		if err != nil {
			return fmt.Errorf("dòng %d: %w", n, err)
		}
		tasks = append(tasks, t)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("đọc stdin: %w", err)
	}

	err := update(file, func(s *task.MemoryStore) error {
		for _, t := range tasks {
			s.Add(t)
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "Đã thêm %d task từ stdin\n", len(tasks))
	return nil
}

// list: taskflow list [-project N] [-status S] [-overdue]
func (a *app) list(file string, args []string) error {
	flags := a.newFlagSet("list", "[-project N] [-status todo|doing|done] [-overdue]")
	projectID := flags.Int("project", 0, "chỉ lấy task của project này (0 là mọi project)")
	overdue := flags.Bool("overdue", false, "chỉ lấy task quá hạn")
	var status *task.Status // nil nghĩa là mọi trạng thái
	flags.Func("status", "chỉ lấy task có trạng thái này: todo, doing hoặc done", func(s string) error {
		st, err := task.ParseStatus(s)
		status = &st
		return err
	})
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return usagef(flags, "thừa tham số %q", flags.Args())
	}

	s, err := loadStore(file)
	if err != nil {
		return err
	}
	now := a.now()
	tasks := slices.Collect(filter(s.All(), func(t *task.Task) bool {
		return (*projectID == 0 || t.ProjectID == *projectID) &&
			(status == nil || t.Status == *status) &&
			(!*overdue || t.IsOverdue(now))
	}))
	printTasks(a.stdout, tasks, now)
	return nil
}

// setStatus: taskflow start <id> và taskflow done <id>
func (a *app) setStatus(file, cmd string, args []string) error {
	flags := a.newFlagSet(cmd, "<id>")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	id, err := idArg(flags)
	if err != nil {
		return err
	}

	var t *task.Task
	err = update(file, func(s *task.MemoryStore) error {
		if t, err = s.Get(id); err != nil {
			return err
		}
		if cmd == "start" {
			t.Start()
		} else {
			t.Complete()
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "Task #%d %q chuyển sang %v\n", t.ID, t.Title, t.Status)
	return nil
}

// assign: taskflow assign -user N <id>
func (a *app) assign(file string, args []string) error {
	flags := a.newFlagSet("assign", "-user N <id>")
	userID := flags.Int("user", 0, "id của người nhận task (bắt buộc)")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	id, err := idArg(flags)
	if err != nil {
		return err
	}
	if *userID <= 0 {
		return usagef(flags, "-user phải lớn hơn 0")
	}

	err = update(file, func(s *task.MemoryStore) error {
		t, err := s.Get(id)
		if err != nil {
			return err
		}
		t.Assign(*userID)
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "Đã giao task #%d cho user #%d\n", id, *userID)
	return nil
}
