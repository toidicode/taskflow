// Package project quản lý các project của TaskFlow.
package project

import "strings"

// Project là một dự án, chứa nhiều task.
type Project struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	OwnerID     int    `json:"owner_id"`
}

// New tạo một project mới của người dùng ownerID, ID do Store cấp khi thêm project.
func New(name string, ownerID int) *Project {
	return &Project{
		Name:    strings.TrimSpace(name),
		OwnerID: ownerID,
	}
}
