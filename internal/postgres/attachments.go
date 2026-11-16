package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/toidicode/taskflow/internal/attachment"
	"github.com/toidicode/taskflow/internal/postgres/dbgen"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/user"
)

// AttachmentStore lưu thông tin file đính kèm trong bảng attachments, cài interface attachment.Store.
type AttachmentStore struct {
	q *dbgen.Queries
}

var _ attachment.Store = (*AttachmentStore)(nil)

// NewAttachmentStore tạo AttachmentStore dùng pool db.
func NewAttachmentStore(db *sql.DB) *AttachmentStore {
	return &AttachmentStore{q: dbgen.New(db)}
}

func (s *AttachmentStore) Add(ctx context.Context, a *attachment.Attachment) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	row, err := s.q.CreateAttachment(ctx, dbgen.CreateAttachmentParams{
		TaskID:      int64(a.TaskID),
		Name:        a.Name,
		StoredName:  a.StoredName,
		ContentType: a.ContentType,
		Size:        a.Size,
		UploadedBy:  nullInt(a.UploadedBy),
	})
	switch violatedForeignKey(err) {
	case "attachments_task_id_fkey": // task bị xóa trong lúc file đang được upload
		return fmt.Errorf("task #%d: %w", a.TaskID, task.ErrNotFound)
	case "attachments_uploaded_by_fkey":
		return fmt.Errorf("user #%d: %w", a.UploadedBy, user.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("thêm attachment: %w", err)
	}
	a.ID, a.CreatedAt = int(row.ID), row.CreatedAt
	return nil
}

func (s *AttachmentStore) Get(ctx context.Context, id int) (attachment.Attachment, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	row, err := s.q.GetAttachment(ctx, int64(id))
	if errors.Is(err, sql.ErrNoRows) {
		return attachment.Attachment{}, fmt.Errorf("attachment #%d: %w", id, attachment.ErrNotFound)
	}
	if err != nil {
		return attachment.Attachment{}, fmt.Errorf("đọc attachment #%d: %w", id, err)
	}
	return toAttachment(row), nil
}

func (s *AttachmentStore) List(ctx context.Context, taskID int) ([]attachment.Attachment, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	rows, err := s.q.ListAttachments(ctx, int64(taskID))
	if err != nil {
		return nil, fmt.Errorf("liệt kê attachment của task #%d: %w", taskID, err)
	}
	list := make([]attachment.Attachment, 0, len(rows))
	for _, row := range rows {
		list = append(list, toAttachment(row))
	}
	return list, nil
}

func (s *AttachmentStore) Delete(ctx context.Context, id int) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	n, err := s.q.DeleteAttachment(ctx, int64(id))
	if err != nil {
		return fmt.Errorf("xóa attachment #%d: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("attachment #%d: %w", id, attachment.ErrNotFound)
	}
	return nil
}

func (s *AttachmentStore) KnownFiles(ctx context.Context, storedNames []string) (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	names, err := s.q.KnownStoredNames(ctx, storedNames)
	if err != nil {
		return nil, fmt.Errorf("tìm file có attachment: %w", err)
	}
	known := make(map[string]bool, len(names))
	for _, name := range names {
		known[name] = true
	}
	return known, nil
}

// toAttachment đổi một hàng của bảng attachments sang attachment.Attachment.
func toAttachment(row dbgen.Attachment) attachment.Attachment {
	return attachment.Attachment{
		ID:          int(row.ID),
		TaskID:      int(row.TaskID),
		Name:        row.Name,
		StoredName:  row.StoredName,
		ContentType: row.ContentType,
		Size:        row.Size,
		UploadedBy:  int(row.UploadedBy.Int64), // NULL thành 0
		CreatedAt:   row.CreatedAt,
	}
}
