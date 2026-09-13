// catalog rebuilds pricing/index.json. Run from the repository root.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/rakunlabs/at/pricing"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	c, err := pricing.LoadProviders(os.DirFS("pricing"))
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode pricing index: %w", err)
	}
	if err := os.WriteFile("pricing/index.json", append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write pricing index: %w", err)
	}
	return nil
}
