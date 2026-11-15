package main

import (
	"fmt"
	"io"
	"iter"
	"slices"
	"strings"
	"time"

	"github.com/toidicode/taskflow/internal/task"
)

// printTasks in danh sách tasks ra w, kèm dòng tổng kết; quá hạn tính tại thời điểm now.
func printTasks(w io.Writer, tasks []task.Task, now time.Time) {
	if len(tasks) == 0 {
		fmt.Fprintln(w, "Không có task nào")
		return
	}
	for _, t := range tasks {
		fmt.Fprintf(w, "#%-2d [p%d] %-18s %-5s %-8s %s\n",
			t.ID, t.ProjectID, t.Title, t.Status, assigneeText(t), dueText(t, now))
	}

	done := slices.Collect(filter(slices.Values(tasks), func(t task.Task) bool { return t.IsDone() }))
	overdue := slices.Collect(filter(slices.Values(tasks), func(t task.Task) bool { return t.IsOverdue(now) }))
	fmt.Fprintf(w, "Đã xong %d/%d task, quá hạn %d task\n", len(done), len(tasks), len(overdue))
	if len(overdue) > 0 {
		titles := mapSeq(slices.Values(overdue), func(t task.Task) string { return t.Title })
		fmt.Fprintln(w, "Cần xử lý gấp:", strings.Join(slices.Collect(titles), ", "))
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

// assigneeText mô tả người nhận task t, VD "user #2".
func assigneeText(t task.Task) string {
	if t.AssigneeID == 0 {
		return "-"
	}
	return fmt.Sprintf("user #%d", t.AssigneeID)
}

// dueText mô tả hạn của task t, VD "hạn 14/11/2026 (quá hạn)".
func dueText(t task.Task, now time.Time) string {
	if t.DueDate.IsZero() {
		return "không có hạn"
	}
	s := "hạn " + t.DueDate.Format("02/01/2006")
	if t.IsOverdue(now) {
		s += " (quá hạn)"
	}
	return s
}
