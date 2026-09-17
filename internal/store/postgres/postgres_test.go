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
	"github.com/rakunlabs/at/internal/store/postgres/pgtemplate"
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

// templatePrefix is the table prefix inside a template-copied database. The
// database is private to one test, so the prefix only has to be stable.
const templatePrefix = "t_"

// TestMain releases the process template database after the suite.
func TestMain(m *testing.M) {
	code := m.Run()
	pgtemplate.Release()
	os.Exit(code)
}

func migrateTemplate(ctx context.Context, dsn string) error {
	prefix := templatePrefix
	store, err := New(ctx, &config.StorePostgres{TablePrefix: &prefix, Datasource: dsn}, nil)
	if err != nil {
		return err
	}
	// The template cannot be copied while a session is connected to it.
	store.Close()
	return nil
}

// newTestStore gives the test its own already-migrated database, copied from a
// per-process template. Without templates (no CREATEDB, unsupported DSN) it
// falls back to a unique table prefix in the shared database, which costs the
// full migration set per test.
func newTestStore(t *testing.T, encKey []byte) *Postgres {
	t.Helper()

	dsn := testDSN()
	pingTestPostgres(t, dsn)

	if copyDSN, release, ok := pgtemplate.Acquire(context.Background(), dsn, migrateTemplate); ok {
		prefix := templatePrefix
		store, err := New(context.Background(), &config.StorePostgres{TablePrefix: &prefix, Datasource: copyDSN}, encKey)
		if err != nil {
			release()
			t.Fatalf("postgres.New: %v", err)
		}
		t.Cleanup(func() { store.Close(); release() })
		return store
	}

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
