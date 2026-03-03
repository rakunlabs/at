package memory

import (
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func paginate[T any](items []T, q *query.Query) *service.ListResult[T] {
	total := uint64(len(items))

	limit := uint64(total)
	if q != nil && q.Limit != nil {
		limit = uint64(*q.Limit)
	}
	// If limit is 0 or less (and not specified), return all.
	// But if limit is specified as > 0, use it.
	if limit <= 0 {
		limit = uint64(total)
		if limit == 0 {
			limit = 1
		}
	}

	offset := uint64(0)
	if q != nil && q.Offset != nil {
		offset = uint64(*q.Offset)
	}

	start := offset
	end := offset + limit

	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	// Safety check
	if end < start {
		end = start
	}

	paged := items[start:end]

	return &service.ListResult[T]{
		Data: paged,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}
}
