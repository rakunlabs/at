// Package postgrestest provides a shared test helper for spinning up an
// isolated AT store against a real postgres database. Tests are skipped
// when postgres is unreachable; `make env` starts a matching instance.
package postgrestest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/store/postgres"
)

// DSN returns the postgres DSN used by tests. Override with
// AT_TEST_POSTGRES_DSN; the default matches `make env` (env/compose.yaml).
func DSN() string {
	if dsn := os.Getenv("AT_TEST_POSTGRES_DSN"); dsn != "" {
		return dsn
	}
	return "postgres://postgres@localhost:5432/postgres?sslmode=disable"
}

// New connects to the test postgres with a unique table prefix so tests are
// isolated from each other and from any dev data. The calling test is
// skipped when postgres is not reachable; tables created for the test are
// dropped on cleanup.
func New(t *testing.T, encKey []byte) *postgres.Postgres {
	t.Helper()

	dsn := DSN()

	// Cheap reachability probe before running the full migration set.
	{
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Skipf("postgres not available at %s (run `make env`): %v", dsn, err)
		}
		if err := db.PingContext(ctx); err != nil {
			db.Close()
			t.Skipf("postgres not available at %s (run `make env`): %v", dsn, err)
		}
		db.Close()
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

// dropTables removes every table created under the test's unique prefix.
func dropTables(t *testing.T, dsn, prefix string) {
	t.Helper()

	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Logf("cleanup: open: %v", err)
		return
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx,
		`SELECT tablename FROM pg_tables WHERE schemaname = current_schema() AND tablename LIKE $1`,
		prefix+"%")
	if err != nil {
		t.Logf("cleanup: list tables: %v", err)
		return
	}
	defer rows.Close()

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
