package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/server"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store"
)

// runAuthCommand must be dispatched before the ordinary server bootstrap.
// It returns handled=false only when no command was requested.
func runAuthCommand(ctx context.Context, args []string) (bool, error) {
	// Only claim the explicit `auth` subcommand. Every other argument, including
	// the server's own configuration flags, belongs to the ordinary bootstrap.
	if len(args) == 0 || args[0] != "auth" {
		return false, nil
	}
	if len(args) < 2 || args[1] != "recover" {
		return true, fmt.Errorf("usage: at auth recover --user-id ID --output FILE")
	}
	fs := flag.NewFlagSet("auth recover", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	id := fs.String("user-id", "", "existing account ID")
	output := fs.String("output", "", "new owner-only token file")
	if err := fs.Parse(args[2:]); err != nil {
		return true, fmt.Errorf("usage: at auth recover --user-id ID --output FILE")
	}
	if *id == "" || *output == "" || fs.NArg() != 0 {
		return true, fmt.Errorf("explicit --user-id and --output are required")
	}
	cfg, err := config.Load(ctx, name)
	if err != nil {
		return true, fmt.Errorf("load recovery configuration: %w", err)
	}
	if cfg.Store.EncryptionKey == "" {
		return true, fmt.Errorf("operator recovery requires configured database encryption authority")
	}
	st, err := store.New(ctx, cfg.Store)
	if err != nil {
		return true, fmt.Errorf("open recovery store: %w", err)
	}
	defer st.Close()
	security, ok := st.(service.AuthSecurityStorer)
	if !ok {
		return true, fmt.Errorf("account security store unavailable")
	}
	// Authentication policy lives in the database now, so recovery asks the
	// store whether this installation is claimed instead of reading YAML.
	settings, ok := st.(service.AuthSettingsStorer)
	if !ok {
		return true, fmt.Errorf("authentication settings store unavailable")
	}
	state, err := settings.GetAuthSettings(ctx)
	if err != nil {
		return true, fmt.Errorf("load authentication settings: %w", err)
	}
	if state == nil || state.SetupRequired {
		return true, fmt.Errorf("operator recovery requires a claimed installation; complete first-run setup instead")
	}
	err = writeRecoveryTicketFile(*output, func() (string, error) { return server.IssueAuthRecoveryTicket(ctx, security, *id, "operator") })
	if err != nil {
		return true, err
	}
	slog.Info("account recovery ticket written", "user_id", *id, "expires_in", 900)
	return true, nil
}

func writeRecoveryTicketFile(path string, issue func() (string, error)) error {
	// O_EXCL refuses existing files, symlinks and hardlinks without ever opening
	// or truncating them. Permissions are owner-only from the first write.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create owner-only recovery output: %w", err)
	}
	success := false
	defer func() {
		f.Close()
		if !success {
			os.Remove(path)
		}
	}()
	ticket, err := issue()
	if err != nil {
		return fmt.Errorf("issue operator recovery: %w", err)
	}
	if _, err := io.WriteString(f, ticket+"\n"); err != nil {
		return fmt.Errorf("write recovery output: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync recovery output: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close recovery output: %w", err)
	}
	success = true
	return nil
}
