package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/config"
)

// testDSN returns the postgres DSN used by store tests. Override with
// AT_TEST_POSTGRES_DSN; the default matches `make env` (env/compose.yaml).
func testDSN() string {
	if dsn := os.Getenv("AT_TEST_POSTGRES_DSN"); dsn != "" {
		return dsn
	}
	return "postgres://postgres@localhost:5432/postgres?sslmode=disable"
}

// pingTestPostgres probes the test database with a short timeout and skips
// the calling test when it is unreachable (run `make env` to start one).
func pingTestPostgres(t *testing.T, dsn string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	db, err := connectDB(ctx, dsn, "")
	if err != nil {
		t.Skipf("postgres not available at %s (run `make env`): %v", dsn, err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("postgres not available at %s (run `make env`): %v", dsn, err)
	}
}

// newTestStore connects to the test postgres with a unique table prefix so
// tests are isolated from each other and from any dev data. Tables created
// for the test are dropped on cleanup.
func newTestStore(t *testing.T, encKey []byte) *Postgres {
	t.Helper()

	dsn := testDSN()
	pingTestPostgres(t, dsn)

	prefix := strings.ToLower("t" + ulid.Make().String() + "_")
	cfg := &config.StorePostgres{
		TablePrefix: &prefix,
		Datasource:  dsn,
	}

	store, err := New(context.Background(), cfg, encKey)
	if err != nil {
		t.Fatalf("postgres.New: %v", err)
	}
	t.Cleanup(func() {
		dropTestTables(t, store, prefix)
		store.Close()
	})

	return store
}

// dropTestTables removes every table created under the test's unique prefix.
func dropTestTables(t *testing.T, store *Postgres, prefix string) {
	t.Helper()
	ctx := context.Background()

	rows, err := store.db.QueryContext(ctx,
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
		if _, err := store.db.ExecContext(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %q CASCADE`, name)); err != nil {
			t.Logf("cleanup: drop %s: %v", name, err)
		}
	}
}
