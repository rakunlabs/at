package config

import (
	"context"
	"fmt"
	"log/slog"

	_ "github.com/rakunlabs/chu/loader/loaderconsul"
	_ "github.com/rakunlabs/chu/loader/loadervault"
	"github.com/rakunlabs/logi"

	"github.com/rakunlabs/chu"
	"github.com/rakunlabs/tell"
)

var Service = ""

type Config struct {
	LogLevel string `cfg:"log_level" default:"info"`

	MCPServerURL string `cfg:"mcp_server_url" default:"http://localhost:8080"`

	SelectLLM string `cfg:"select_llm" default:"antropic"`
	LLM       LLM    `cfg:"llm"`

	Server    Server      `cfg:"server"`
	Telemetry tell.Config `cfg:"telemetry"`
}

type Server struct {
	BasePath string `cfg:"base_path"`

	Port string `cfg:"port" default:"8080"`
	Host string `cfg:"host"`
}

type Store struct {
	Postgres *StorePostgres `cfg:"postgres"`
}

type StorePostgres struct {
	TablePrefix  string  `cfg:"table_prefix"`
	DBDatasource string  `cfg:"db_datasource" log:"-"`
	DBSchema     string  `cfg:"db_schema"`
	Migrate      Migrate `cfg:"migrate"`
}

type Migrate struct {
	DBDatasource string            `cfg:"db_datasource" log:"-"`
	DBSchema     string            `cfg:"db_schema"`
	DBTable      string            `cfg:"db_table"`
	Values       map[string]string `cfg:"values"`
}

type LLM struct {
	Antropic Antropic `cfg:"antropic"`
}

type Antropic struct {
	APIKey string `cfg:"api_key"`
	Model  string `cfg:"model" default:"claude-haiku-4-5"`
}

func Load(ctx context.Context, path string) (*Config, error) {
	var cfg Config
	if err := chu.Load(ctx, path, &cfg); err != nil {
		return nil, err
	}

	if err := logi.SetLogLevel(cfg.LogLevel); err != nil {
		return nil, fmt.Errorf("set log level %s: %w", cfg.LogLevel, err)
	}

	slog.Info("loaded configuration", "config", chu.MarshalMap(cfg))

	return &cfg, nil
}
