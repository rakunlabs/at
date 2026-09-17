// Package pgtemplate gives store-backed tests a private, already-migrated
// database without paying for the migration set every time.
//
// Running the full migration set costs about 360ms and dropping the ~80 tables
// it creates another 165ms, so a suite with a hundred fixtures spent a minute
// of wall time on setup alone. Postgres can copy a prepared database in about
// 40ms (CREATE DATABASE ... TEMPLATE) and drop it in one statement, so the set
// is applied once per process into a template and every test gets a copy.
//
// A copy is a real database, so isolation is stronger than the table-prefix
// scheme it replaces: a test cannot see another test's tables at all.
package pgtemplate

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/oklog/ulid/v2"
)

const (
	templatePrefix = "at_test_tmpl_"
	copyPrefix     = "at_test_db_"
	// staleAfter bounds what a crashed run can leave behind. It must be longer
	// than any plausible suite run, because a live template belonging to a
	// concurrent process is indistinguishable from an abandoned one: neither
	// holds a connection while it is being copied.
	staleAfter = 2 * time.Hour
)

var (
	mu       sync.Mutex
	prepared bool
	admin    *sql.DB
	template string
	base     string
)

// Acquire returns a DSN for a private already-migrated database and the
// function that drops it. ok is false when templates are unavailable — a DSN
// shape this package cannot rewrite, a role without CREATEDB, or an unreachable
// server — and the caller must fall back to its own setup.
//
// migrate is called once per process with the template's DSN and must leave the
// schema in the state tests expect.
func Acquire(ctx context.Context, dsn string, migrate func(context.Context, string) error) (string, func(), bool) {
	mu.Lock()
	defer mu.Unlock()
	if !prepare(ctx, dsn, migrate) {
		return "", nil, false
	}
	name := copyPrefix + newID()
	if _, err := admin.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", name, template)); err != nil {
		return "", nil, false
	}
	copyDSN, ok := withDatabase(base, name)
	if !ok {
		drop(name)
		return "", nil, false
	}
	return copyDSN, func() { drop(name) }, true
}

// Release drops the process template. TestMain calls it after m.Run; anything a
// crashed run leaves behind is swept by age on the next run.
func Release() {
	mu.Lock()
	defer mu.Unlock()
	if !prepared {
		return
	}
	drop(template)
	_ = admin.Close()
	prepared, admin, template = false, nil, ""
}

// prepare builds the template once. Failures are sticky for the process: a role
// without CREATEDB will not grow the permission between tests, and retrying per
// fixture would pay the failure cost every time.
func prepare(ctx context.Context, dsn string, migrate func(context.Context, string) error) bool {
	if prepared {
		return true
	}
	if _, ok := withDatabase(dsn, "probe"); !ok {
		return false
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return false
	}
	if err = db.PingContext(ctx); err != nil {
		_ = db.Close()
		return false
	}
	name := templatePrefix + newID()
	if _, err = db.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		_ = db.Close()
		return false
	}
	templateDSN, _ := withDatabase(dsn, name)
	// Migration owns its own connections and must close them: a template cannot
	// be copied while any session is connected to it.
	if err = migrate(ctx, templateDSN); err != nil {
		_, _ = db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		_ = db.Close()
		return false
	}
	admin, template, base, prepared = db, name, dsn, true
	sweep(ctx)
	return true
}

// sweep removes databases from runs that died before releasing them.
func sweep(ctx context.Context) {
	rows, err := admin.QueryContext(ctx, "SELECT datname FROM pg_database WHERE datname LIKE $1 OR datname LIKE $2", templatePrefix+"%", copyPrefix+"%")
	if err != nil {
		return
	}
	var stale []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil || name == template {
			continue
		}
		id, err := ulid.ParseStrict(strings.ToUpper(name[strings.LastIndex(name, "_")+1:]))
		if err != nil || time.Since(ulid.Time(id.Time())) < staleAfter {
			continue
		}
		stale = append(stale, name)
	}
	_ = rows.Close()
	for _, name := range stale {
		drop(name)
	}
}

func drop(name string) {
	if admin == nil {
		return
	}
	// FORCE terminates leftover sessions; a pool that has not finished closing
	// would otherwise make the drop fail and leak the database.
	_, _ = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
}

func newID() string {
	return strings.ToLower(ulid.Make().String())
}

// withDatabase rewrites the database of a URL-style DSN. Keyword/value DSNs are
// reported as unsupported rather than guessed at.
func withDatabase(dsn, database string) (string, bool) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		return "", false
	}
	u.Path = "/" + database
	return u.String(), true
}
