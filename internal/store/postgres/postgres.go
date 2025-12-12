package postgres

import (
	"context"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

var (
	ConnMaxLifetime = 15 * time.Minute
	MaxIdleConns    = 3
	MaxOpenConns    = 3
)

var dbType = "pgx"

type Postgres struct {
	db *sqlx.DB
}

func New(ctx context.Context, dsn string) (*Postgres, error) {
	db, err := sqlx.ConnectContext(ctx, dbType, dsn)
	if err != nil {
		return nil, err
	}

	db.SetConnMaxLifetime(ConnMaxLifetime)
	db.SetMaxIdleConns(MaxIdleConns)
	db.SetMaxOpenConns(MaxOpenConns)

	return &Postgres{
		db: db,
	}, nil
}
