# internal/store — Data Layer

## Purpose

Store factory and three backend implementations. All implement `StorerClose` which composes every store interface from `service` package + `Close()`.

## Key Files

- `store.go` — Factory: `New(ctx, cfg)` → postgres > sqlite3 > memory fallback. Derives AES key from config passphrase.

## Backends

| Directory | Backend | Notes |
|---|---|---|
| `sqlite3/` | SQLite (modernc.org/sqlite) | WAL + foreign keys auto-enabled. Recommended for single-instance. |
| `postgres/` | PostgreSQL (pgx/v5) | Configurable schema, pool settings. Uses goqu query builder. |
| `memory/` | In-memory | Volatile. No encryption support. Default fallback. |

## Data Entities

All backends implement CRUD for: providers, api_tokens, workflows, triggers, skills, variables, node_configs.

Table prefix: configurable, defaults to `at_`.

## Encryption

- AES-256-GCM for provider credentials (`api_key`, `extra_headers`)
- Key derived: `SHA-256(config.encryption_key)` → 32-byte AES key (see `internal/crypto/`)
- Encrypted values prefixed with `enc:` in DB
- `KeyRotator` interface: re-encrypts all credentials in a single DB transaction
- `EncryptionKeyUpdater`: updates in-memory key without restart (used by cluster broadcast)
- In-memory store: no encryption (data never persisted)

## Store Initialization Flow

```
cmd/at/main.go → store.New(ctx, cfg)
  → if cfg.Postgres != nil → postgres.New(ctx, pgCfg, encKey)
  → else if cfg.SQLite != nil → sqlite3.New(ctx, sqliteCfg, encKey)
  → else → memory.New()
```

## Patterns

- goqu query builder for SQL generation (sqlite3 + postgres)
- Migrations embedded in each backend package
- Not-found: `errors.Is(err, sql.ErrNoRows)` → return `nil, nil`
- Error wrapping: `fmt.Errorf("store operation: %w", err)`
