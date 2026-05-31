package sqlite3

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

type connectorRow struct {
	Slug        string `db:"slug"`
	Name        string `db:"name"`
	Description string `db:"description"`
	Icon        string `db:"icon"`
	AuthKind    string `db:"auth_kind"`
	OAuth       string `db:"oauth"`
	Fields      string `db:"fields"`
	CreatedAt   string `db:"created_at"`
	UpdatedAt   string `db:"updated_at"`
	CreatedBy   string `db:"created_by"`
	UpdatedBy   string `db:"updated_by"`
}

var connectorColumns = []string{
	"slug", "name", "description", "icon", "auth_kind", "oauth", "fields",
	"created_at", "updated_at", "created_by", "updated_by",
}

func (s *SQLite) ListConnectors(ctx context.Context, q *query.Query) (*service.ListResult[service.Connector], error) {
	sqlStr, total, err := s.buildListQuery(ctx, s.tableConnectors, q, toAnySlice(connectorColumns)...)
	if err != nil {
		return nil, fmt.Errorf("build list connectors query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, sqlStr)
	if err != nil {
		return nil, fmt.Errorf("list connectors: %w", err)
	}
	defer rows.Close()

	var items []service.Connector
	for rows.Next() {
		var row connectorRow
		if err := scanConnectorRow(rows, &row); err != nil {
			return nil, err
		}
		rec, err := connectorRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.Connector]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (s *SQLite) GetConnector(ctx context.Context, slug string) (*service.Connector, error) {
	sqlStr, _, err := s.goqu.From(s.tableConnectors).
		Select(toAnySlice(connectorColumns)...).
		Where(goqu.I("slug").Eq(slug)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get connector query: %w", err)
	}

	var row connectorRow
	err = scanConnectorRow(s.db.QueryRowContext(ctx, sqlStr), &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get connector %q: %w", slug, err)
	}

	return connectorRowToRecord(row)
}

func (s *SQLite) CreateConnector(ctx context.Context, c service.Connector) (*service.Connector, error) {
	oauthStr, fieldsStr, err := marshalConnectorParts(c)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)

	sqlStr, _, err := s.goqu.Insert(s.tableConnectors).Rows(
		goqu.Record{
			"slug":        c.Slug,
			"name":        c.Name,
			"description": c.Description,
			"icon":        c.Icon,
			"auth_kind":   c.AuthKind,
			"oauth":       oauthStr,
			"fields":      fieldsStr,
			"created_at":  now,
			"updated_at":  now,
			"created_by":  c.CreatedBy,
			"updated_by":  c.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert connector query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, sqlStr); err != nil {
		return nil, fmt.Errorf("create connector %q: %w", c.Slug, err)
	}

	return s.GetConnector(ctx, c.Slug)
}

func (s *SQLite) UpdateConnector(ctx context.Context, slug string, c service.Connector) (*service.Connector, error) {
	oauthStr, fieldsStr, err := marshalConnectorParts(c)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)

	sqlStr, _, err := s.goqu.Update(s.tableConnectors).Set(
		goqu.Record{
			"name":        c.Name,
			"description": c.Description,
			"icon":        c.Icon,
			"auth_kind":   c.AuthKind,
			"oauth":       oauthStr,
			"fields":      fieldsStr,
			"updated_at":  now,
			"updated_by":  c.UpdatedBy,
		},
	).Where(goqu.I("slug").Eq(slug)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update connector query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, sqlStr)
	if err != nil {
		return nil, fmt.Errorf("update connector %q: %w", slug, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return s.GetConnector(ctx, slug)
}

func (s *SQLite) DeleteConnector(ctx context.Context, slug string) error {
	sqlStr, _, err := s.goqu.Delete(s.tableConnectors).
		Where(goqu.I("slug").Eq(slug)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete connector query: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, sqlStr); err != nil {
		return fmt.Errorf("delete connector %q: %w", slug, err)
	}
	return nil
}

// ─── Helpers ───

// rowScanner abstracts *sql.Row and *sql.Rows for shared scan logic.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanConnectorRow(sc rowScanner, row *connectorRow) error {
	return sc.Scan(&row.Slug, &row.Name, &row.Description, &row.Icon, &row.AuthKind,
		&row.OAuth, &row.Fields, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
}

func marshalConnectorParts(c service.Connector) (oauthStr, fieldsStr string, err error) {
	oauthBytes := []byte("{}")
	if c.OAuth != nil {
		oauthBytes, err = json.Marshal(c.OAuth)
		if err != nil {
			return "", "", fmt.Errorf("marshal connector oauth: %w", err)
		}
	}
	fieldsBytes := []byte("[]")
	if len(c.Fields) > 0 {
		fieldsBytes, err = json.Marshal(c.Fields)
		if err != nil {
			return "", "", fmt.Errorf("marshal connector fields: %w", err)
		}
	}
	return string(oauthBytes), string(fieldsBytes), nil
}

func connectorRowToRecord(row connectorRow) (*service.Connector, error) {
	c := service.Connector{
		Slug:        row.Slug,
		Name:        row.Name,
		Description: row.Description,
		Icon:        row.Icon,
		AuthKind:    row.AuthKind,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		CreatedBy:   row.CreatedBy,
		UpdatedBy:   row.UpdatedBy,
	}

	if row.OAuth != "" && row.OAuth != "{}" && row.OAuth != "null" {
		var oauth service.ConnectorOAuth
		if err := json.Unmarshal([]byte(row.OAuth), &oauth); err != nil {
			return nil, fmt.Errorf("unmarshal connector oauth for %q: %w", row.Slug, err)
		}
		c.OAuth = &oauth
	}
	if row.Fields != "" && row.Fields != "null" {
		if err := json.Unmarshal([]byte(row.Fields), &c.Fields); err != nil {
			return nil, fmt.Errorf("unmarshal connector fields for %q: %w", row.Slug, err)
		}
	}

	return &c, nil
}

// toAnySlice converts []string to []any for goqu's variadic Select.
func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
