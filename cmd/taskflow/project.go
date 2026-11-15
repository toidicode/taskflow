package main

import "strings"

// Project là một dự án, chứa nhiều task.
type Project struct {
	ID          int
	Name        string
	Description string
	OwnerID     int
}

// NewProject tạo một project mới của người dùng ownerID.
func NewProject(id int, name string, ownerID int) *Project {
	return &Project{
		ID:      id,
		Name:    strings.TrimSpace(name),
		OwnerID: ownerID,
	}
}
