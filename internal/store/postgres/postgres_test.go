package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/store/postgres/pgtemplate"
	"github.com/rakunlabs/at/internal/store/postgres/pgtestdb"
)

// testDSN returns the postgres DSN used by store tests, resolved by pgtestdb
// (AT_TEST_POSTGRES_DSN, `make env`, or a testcontainers postgres). The calling
// test is skipped when no server is available.
func testDSN(t *testing.T) string {
	t.Helper()
	return pgtestdb.Require(t)
}

// templatePrefix is the table prefix inside a template-copied database. The
// database is private to one test, so the prefix only has to be stable.
const templatePrefix = "t_"

// TestMain releases the process template database and the test container
// after the suite.
func TestMain(m *testing.M) {
	code := m.Run()
	pgtemplate.Release()
	pgtestdb.Release()
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

	dsn := testDSN(t)

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
	defer rows.Close() //nolint:errcheck

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
