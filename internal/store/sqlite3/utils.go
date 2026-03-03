package sqlite3

import (
	"context"

	"github.com/doug-martin/goqu/v9"
	"github.com/rakunlabs/query"
	"github.com/rakunlabs/query/adapter/adaptergoqu"
)

func (s *SQLite) buildListQuery(
	ctx context.Context,
	table interface{}, // table name (string) or identifier
	q *query.Query,
	cols ...interface{},
) (string, uint64, error) {
	ds := s.goqu.From(table)

	// 1. Calculate Total Count (applying only Where)
	countDs := ds
	if q != nil {
		if exprs := adaptergoqu.Expression(q); len(exprs) > 0 {
			countDs = countDs.Where(exprs...)
		}
	}

	countSQL, _, err := countDs.Select(goqu.COUNT("*")).ToSQL()
	if err != nil {
		return "", 0, err
	}

	var total uint64
	if err := s.db.QueryRowContext(ctx, countSQL).Scan(&total); err != nil {
		return "", 0, err
	}

	// 2. Build Data Query (Where + Sort + Limit + Offset)
	// adaptergoqu.Select applies Where, Sort, Limit, Offset
	ds = adaptergoqu.Select(q, ds, adaptergoqu.WithParameterized(false))
	querySQL, _, err := ds.Select(cols...).ToSQL()

	return querySQL, total, err
}

func getPagination(q *query.Query) (uint64, uint64) {
	if q == nil {
		return 0, 0
	}

	return q.GetOffset(), q.GetLimit()
}
