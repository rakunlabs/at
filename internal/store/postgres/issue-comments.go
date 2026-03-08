package postgres

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

// ─── Issue Comments ───

type issueCommentRow struct {
	ID         string         `db:"id"`
	TaskID     string         `db:"task_id"`
	AuthorType string         `db:"author_type"`
	AuthorID   string         `db:"author_id"`
	Body       string         `db:"body"`
	ParentID   sql.NullString `db:"parent_id"`
	CreatedAt  time.Time      `db:"created_at"`
	UpdatedAt  time.Time      `db:"updated_at"`
}

var issueCommentColumns = []interface{}{
	"id", "task_id", "author_type", "author_id", "body", "parent_id",
	"created_at", "updated_at",
}

func scanIssueCommentRow(scanner interface {
	Scan(dest ...interface{}) error
}, row *issueCommentRow) error {
	return scanner.Scan(
		&row.ID, &row.TaskID, &row.AuthorType, &row.AuthorID,
		&row.Body, &row.ParentID, &row.CreatedAt, &row.UpdatedAt,
	)
}

func (p *Postgres) ListCommentsByTask(ctx context.Context, taskID string) ([]service.IssueComment, error) {
	query, _, err := p.goqu.From(p.tableIssueComments).
		Select(issueCommentColumns...).
		Where(goqu.I("task_id").Eq(taskID)).
		Order(goqu.I("created_at").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list comments by task query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list comments by task %q: %w", taskID, err)
	}
	defer rows.Close()

	var items []service.IssueComment
	for rows.Next() {
		var row issueCommentRow
		if err := scanIssueCommentRow(rows, &row); err != nil {
			return nil, fmt.Errorf("scan issue comment row: %w", err)
		}

		items = append(items, *issueCommentRowToRecord(row))
	}

	return items, rows.Err()
}

func (p *Postgres) GetComment(ctx context.Context, id string) (*service.IssueComment, error) {
	query, _, err := p.goqu.From(p.tableIssueComments).
		Select(issueCommentColumns...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get comment query: %w", err)
	}

	var row issueCommentRow
	err = scanIssueCommentRow(p.db.QueryRowContext(ctx, query), &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get comment %q: %w", id, err)
	}

	return issueCommentRowToRecord(row), nil
}

func (p *Postgres) CreateComment(ctx context.Context, comment service.IssueComment) (*service.IssueComment, error) {
	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := p.goqu.Insert(p.tableIssueComments).Rows(
		goqu.Record{
			"id":          id,
			"task_id":     comment.TaskID,
			"author_type": comment.AuthorType,
			"author_id":   comment.AuthorID,
			"body":        comment.Body,
			"parent_id":   nullString(comment.ParentID),
			"created_at":  now,
			"updated_at":  now,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert comment query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create comment: %w", err)
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

func (p *Postgres) UpdateComment(ctx context.Context, id string, comment service.IssueComment) (*service.IssueComment, error) {
	now := time.Now().UTC()

	query, _, err := p.goqu.Update(p.tableIssueComments).Set(
		goqu.Record{
			"body":       comment.Body,
			"updated_at": now,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update comment query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, query)
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

	return p.GetComment(ctx, id)
}

func (p *Postgres) DeleteComment(ctx context.Context, id string) error {
	query, _, err := p.goqu.Delete(p.tableIssueComments).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete comment query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete comment %q: %w", id, err)
	}

	return nil
}

func issueCommentRowToRecord(row issueCommentRow) *service.IssueComment {
	return &service.IssueComment{
		ID:         row.ID,
		TaskID:     row.TaskID,
		AuthorType: row.AuthorType,
		AuthorID:   row.AuthorID,
		Body:       row.Body,
		ParentID:   row.ParentID.String,
		CreatedAt:  row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  row.UpdatedAt.Format(time.RFC3339),
	}
}
