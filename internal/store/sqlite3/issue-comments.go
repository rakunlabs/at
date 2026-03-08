package sqlite3

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
)

type issueCommentRow struct {
	ID         string         `db:"id"`
	TaskID     string         `db:"task_id"`
	AuthorType string         `db:"author_type"`
	AuthorID   string         `db:"author_id"`
	Body       string         `db:"body"`
	ParentID   sql.NullString `db:"parent_id"`
	CreatedAt  string         `db:"created_at"`
	UpdatedAt  string         `db:"updated_at"`
}

var issueCommentColumns = []interface{}{"id", "task_id", "author_type", "author_id", "body", "parent_id", "created_at", "updated_at"}

func scanIssueCommentRow(scanner interface{ Scan(dest ...any) error }) (issueCommentRow, error) {
	var row issueCommentRow
	err := scanner.Scan(&row.ID, &row.TaskID, &row.AuthorType, &row.AuthorID, &row.Body, &row.ParentID, &row.CreatedAt, &row.UpdatedAt)

	return row, err
}

func (s *SQLite) ListCommentsByTask(ctx context.Context, taskID string) ([]service.IssueComment, error) {
	query, _, err := s.goqu.From(s.tableIssueComments).
		Select(issueCommentColumns...).
		Where(goqu.I("task_id").Eq(taskID)).
		Order(goqu.I("created_at").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list comments by task query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list comments for task %q: %w", taskID, err)
	}
	defer rows.Close()

	var items []service.IssueComment
	for rows.Next() {
		row, err := scanIssueCommentRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan issue comment row: %w", err)
		}

		items = append(items, issueCommentRowToRecord(row))
	}

	return items, rows.Err()
}

func (s *SQLite) GetComment(ctx context.Context, id string) (*service.IssueComment, error) {
	query, _, err := s.goqu.From(s.tableIssueComments).
		Select(issueCommentColumns...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get comment query: %w", err)
	}

	row, err := scanIssueCommentRow(s.db.QueryRowContext(ctx, query))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get comment %q: %w", id, err)
	}

	comment := issueCommentRowToRecord(row)

	return &comment, nil
}

func (s *SQLite) CreateComment(ctx context.Context, comment service.IssueComment) (*service.IssueComment, error) {
	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := s.goqu.Insert(s.tableIssueComments).Rows(
		goqu.Record{
			"id":          id,
			"task_id":     comment.TaskID,
			"author_type": comment.AuthorType,
			"author_id":   comment.AuthorID,
			"body":        comment.Body,
			"parent_id":   comment.ParentID,
			"created_at":  now.Format(time.RFC3339),
			"updated_at":  now.Format(time.RFC3339),
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert comment query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create comment for task %q: %w", comment.TaskID, err)
	}

	return &service.IssueComment{
		ID:         id,
		TaskID:     comment.TaskID,
		AuthorType: comment.AuthorType,
		AuthorID:   comment.AuthorID,
		Body:       comment.Body,
		ParentID:   comment.ParentID,
		CreatedAt:  now.Format(time.RFC3339),
		UpdatedAt:  now.Format(time.RFC3339),
	}, nil
}

func (s *SQLite) UpdateComment(ctx context.Context, id string, comment service.IssueComment) (*service.IssueComment, error) {
	now := time.Now().UTC()

	query, _, err := s.goqu.Update(s.tableIssueComments).Set(
		goqu.Record{
			"body":       comment.Body,
			"updated_at": now.Format(time.RFC3339),
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update comment query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update comment %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return s.GetComment(ctx, id)
}

func (s *SQLite) DeleteComment(ctx context.Context, id string) error {
	query, _, err := s.goqu.Delete(s.tableIssueComments).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete comment query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete comment %q: %w", id, err)
	}

	return nil
}

func issueCommentRowToRecord(row issueCommentRow) service.IssueComment {
	return service.IssueComment{
		ID:         row.ID,
		TaskID:     row.TaskID,
		AuthorType: row.AuthorType,
		AuthorID:   row.AuthorID,
		Body:       row.Body,
		ParentID:   row.ParentID.String,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}
}
