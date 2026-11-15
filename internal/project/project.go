// Package project quản lý các project của TaskFlow.
package project

import "strings"

// Project là một dự án, chứa nhiều task.
type Project struct {
	ID          int
	Name        string
	Description string
	OwnerID     int
}

// New tạo một project mới của người dùng ownerID.
func New(id int, name string, ownerID int) *Project {
	return &Project{
		ID:      id,
		Name:    strings.TrimSpace(name),
		OwnerID: ownerID,
	}
}
