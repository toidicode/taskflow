// Command taskflow là ứng dụng CLI quản lý công việc của TaskFlow.
package main

import (
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

//go:embed usage.txt
var usage string

// Hai lỗi đặc biệt: thông báo đã được in ra, run chỉ cần chọn exit code.
var (
	errUsage = errors.New("sai cú pháp")     // exit code 2
	errHelp  = errors.New("đã in hướng dẫn") // exit code 0
)

// app gom những thứ chương trình lấy từ bên ngoài, để test thay được.
type app struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	now    func() time.Time
}

func main() {
	a := &app{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, now: time.Now}
	os.Exit(a.run(os.Args[1:]))
}

// run chạy lệnh trong args (không gồm tên chương trình) và trả về exit code.
func (a *app) run(args []string) int {
	err := a.dispatch(args)
	switch {
	case err == nil, errors.Is(err, errHelp):
		return 0
	case errors.Is(err, errUsage):
		return 2
	default:
		fmt.Fprintln(a.stderr, "taskflow:", err)
		return 1
	}
}

// dispatch đọc flag chung rồi gọi lệnh con tương ứng.
func (a *app) dispatch(args []string) error {
	global := flag.NewFlagSet("taskflow", flag.ContinueOnError)
	global.SetOutput(a.stderr)
	global.Usage = func() { fmt.Fprint(a.stderr, usage) }
	file := global.String("file", defaultFile(), "file JSON lưu task")
	if err := parseFlags(global, args); err != nil {
		return err
	}
	if global.NArg() == 0 {
		global.Usage()
		return errUsage
	}

	cmd, rest := global.Arg(0), global.Args()[1:]
	switch cmd {
	case "add":
		return a.add(*file, rest)
	case "import":
		return a.importTasks(*file, rest)
	case "list":
		return a.list(*file, rest)
	case "start", "done":
		return a.setStatus(*file, cmd, rest)
	case "assign":
		return a.assign(*file, rest)
	case "version":
		fmt.Fprintln(a.stdout, "taskflow", version)
		return nil
	case "help":
		fmt.Fprint(a.stdout, usage)
		return nil
	default:
		fmt.Fprintf(a.stderr, "taskflow: không có lệnh %q\n", cmd)
		global.Usage()
		return errUsage
	}
}

// parseFlags gọi flags.Parse và đổi lỗi của package flag thành errHelp/errUsage.
// Với ContinueOnError, flag đã tự in lỗi và cách dùng trước khi trả lỗi về.
func parseFlags(flags *flag.FlagSet, args []string) error {
	err := flags.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return errHelp
	}
	if err != nil {
		return errUsage
	}
	return nil
}
