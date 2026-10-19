// Package pgtestdb resolves the postgres server store-backed tests run against.
//
// Resolution order:
//  1. AT_TEST_POSTGRES_DSN, used as given.
//  2. The `make env` instance on localhost:5432, when it answers (skipped when
//     AT_TEST_POSTGRES_CONTAINER is set, e.g. when port 5432 belongs to an
//     unrelated server).
//  3. A disposable postgres container started through testcontainers, once
//     per test process and terminated by Release (Ryuk reaps it if the process
//     dies first).
//
// When none of them is available the caller skips, unless
// AT_TEST_POSTGRES_REQUIRED is set: CI sets it so a missing Docker daemon fails
// the run instead of silently skipping every database test.
package pgtestdb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	tclog "github.com/testcontainers/testcontainers-go/log"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const (
	// Image matches env/compose.yaml so local and CI runs use one server version.
	Image = "postgres:15-alpine"

	defaultDSN = "postgres://postgres@localhost:5432/postgres?sslmode=disable"
)

var (
	mu        sync.Mutex
	resolved  bool
	dsn       string
	errDSN    error
	container *tcpostgres.PostgresContainer
)

// DSN returns the test server's DSN, starting a container on first use when no
// server is configured or running locally.
func DSN(ctx context.Context) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if !resolved {
		dsn, errDSN = resolve(ctx)
		resolved = true
	}
	return dsn, errDSN
}

// Require returns the test server's DSN, skipping the test when none is
// available (or failing it when AT_TEST_POSTGRES_REQUIRED is set).
func Require(t testing.TB) string {
	t.Helper()
	got, err := DSN(context.Background())
	if err != nil {
		if required() {
			t.Fatalf("postgres required but not available: %v", err)
		}
		t.Skipf("postgres not available (set AT_TEST_POSTGRES_DSN, run `make env` or start Docker): %v", err)
	}
	return got
}

// Release terminates the container started by DSN, if any. Call it from
// TestMain after m.Run.
func Release() {
	mu.Lock()
	defer mu.Unlock()
	if container != nil {
		_ = testcontainers.TerminateContainer(container)
	}
	container, resolved, dsn, errDSN = nil, false, "", nil
}

func required() bool {
	v, _ := strconv.ParseBool(os.Getenv("AT_TEST_POSTGRES_REQUIRED"))
	return v
}

func resolve(ctx context.Context) (string, error) {
	if v := os.Getenv("AT_TEST_POSTGRES_DSN"); v != "" {
		if err := ping(ctx, v); err != nil {
			return "", fmt.Errorf("AT_TEST_POSTGRES_DSN unreachable: %w", err)
		}
		return v, nil
	}
	if force, _ := strconv.ParseBool(os.Getenv("AT_TEST_POSTGRES_CONTAINER")); !force && ping(ctx, defaultDSN) == nil {
		return defaultDSN, nil
	}
	return start(ctx)
}

func start(ctx context.Context) (_ string, err error) {
	// testcontainers panics when no Docker provider is found; report it as an
	// unavailable server instead.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("start postgres container: %v", r)
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	c, err := tcpostgres.Run(ctx, Image,
		tcpostgres.WithDatabase("postgres"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
		testcontainers.WithLogger(tclog.NewNoopLogger()),
	)
	if err != nil {
		if c != nil {
			_ = testcontainers.TerminateContainer(c)
		}
		return "", fmt.Errorf("start postgres container: %w", err)
	}
	got, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		return "", fmt.Errorf("postgres container DSN: %w", err)
	}
	container = c
	return got, nil
}

func ping(ctx context.Context, dsn string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close() //nolint:errcheck
	return db.PingContext(ctx)
}
