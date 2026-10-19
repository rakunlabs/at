// Package postgrestest provides a shared test helper for spinning up an
// isolated AT store against a real postgres database. Without a configured or
// local server a postgres container is started (see pgtestdb); tests are
// skipped only when that is impossible too.
package postgrestest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/store/postgres"
	"github.com/rakunlabs/at/internal/store/postgres/pgtemplate"
	"github.com/rakunlabs/at/internal/store/postgres/pgtestdb"
)

// New gives the test its own migrated store. The server is resolved by
// pgtestdb (AT_TEST_POSTGRES_DSN, `make env`, or a testcontainers postgres);
// the calling test is skipped when none is available.
func New(t *testing.T, encKey []byte) *postgres.Postgres {
	t.Helper()

	dsn := pgtestdb.Require(t)

	// A copy of the process template is a private database created in about
	// 40ms, against 360ms for the migration set plus 165ms to drop its tables.
	if copyDSN, release, ok := pgtemplate.Acquire(context.Background(), dsn, migrateTemplate); ok {
		prefix := templatePrefix
		store, err := postgres.New(context.Background(), &config.StorePostgres{TablePrefix: &prefix, Datasource: copyDSN}, encKey)
		if err != nil {
			release()
			t.Fatalf("postgres.New: %v", err)
		}
		t.Cleanup(func() { store.Close(); release() })
		return store
	}

	prefix := strings.ToLower("t" + ulid.Make().String() + "_")
	store, err := postgres.New(context.Background(), &config.StorePostgres{
		TablePrefix: &prefix,
		Datasource:  dsn,
	}, encKey)
	if err != nil {
		t.Fatalf("postgres.New: %v", err)
	}
	t.Cleanup(func() {
		dropTables(t, dsn, prefix)
		store.Close()
	})

	return store
}

// templatePrefix is the table prefix inside a template-copied database. The
// database belongs to one test, so the prefix only has to be stable.
const templatePrefix = "t_"

func migrateTemplate(ctx context.Context, dsn string) error {
	prefix := templatePrefix
	store, err := postgres.New(ctx, &config.StorePostgres{TablePrefix: &prefix, Datasource: dsn}, nil)
	if err != nil {
		return err
	}
	// The template cannot be copied while a session is connected to it.
	store.Close()
	return nil
}

// Release drops the process template database and stops the test container,
// if one was started. Call it from TestMain after m.Run; packages that never
// call it leave one database per run behind until the next run sweeps it by
// age.
func Release() {
	pgtemplate.Release()
	pgtestdb.Release()
}

// dropTables removes every table created under the test's unique prefix.
func dropTables(t *testing.T, dsn, prefix string) {
	t.Helper()

	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Logf("cleanup: open: %v", err)
		return
	}
	defer db.Close() //nolint:errcheck

	rows, err := db.QueryContext(ctx,
		`SELECT tablename FROM pg_tables WHERE schemaname = current_schema() AND tablename LIKE $1`,
		prefix+"%")
	if err != nil {
		t.Logf("cleanup: list tables: %v", err)
		return
	}
	defer rows.Close() //nolint:errcheck

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			tables = append(tables, name)
		}
	}

	for _, name := range tables {
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %q CASCADE`, name)); err != nil {
			t.Logf("cleanup: drop %s: %v", name, err)
		}
	}
}
