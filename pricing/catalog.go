// Package pricing defines AT's versioned, repository-maintained price catalog.
package pricing

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const URL = "https://raw.githubusercontent.com/rakunlabs/at/main/pricing/index.json"

type Catalog struct {
	Version   int        `json:"version"`
	Currency  string     `json:"currency"`
	Unit      string     `json:"unit"`
	Providers []Provider `json:"providers"`
}

type Provider struct {
	Provider string  `json:"provider"`
	Models   []Model `json:"models"`
}

type Model struct {
	Model      string   `json:"model"`
	Aliases    []string `json:"aliases,omitempty"`
	SourceURL  string   `json:"source_url"`
	VerifiedAt string   `json:"verified_at"`
	Notes      string   `json:"notes"`
	// Conditional rates require an explicit mapping in the Pricing editor.
	ManualOnly bool     `json:"manual_only,omitempty"`
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
}

// Complete distinguishes unknown prices from explicitly free operations.
// The effective-price table cannot represent unknowns, so incomplete entries
// remain in the repository catalog but are never offered for sync.
func (m Model) Complete() bool {
	return m.Input != nil && m.Output != nil && m.CacheRead != nil && m.CacheWrite != nil
}

func decode(r io.Reader, target any) error {
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return fmt.Errorf("decode pricing JSON: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("pricing JSON must contain exactly one document")
	}
	return nil
}

func Parse(r io.Reader) (Catalog, error) {
	var c Catalog
	if err := decode(r, &c); err != nil {
		return c, err
	}
	return c, c.Validate()
}

var slug = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func (c Catalog) Validate() error {
	if c.Version != 1 || c.Currency != "USD" || c.Unit != "per_1m_tokens" || len(c.Providers) == 0 {
		return fmt.Errorf("expected nonempty pricing catalog v1 in USD per_1m_tokens")
	}
	providers := map[string]bool{}
	for _, p := range c.Providers {
		if !slug.MatchString(p.Provider) || providers[p.Provider] || len(p.Models) == 0 {
			return fmt.Errorf("invalid, duplicate or empty provider %q", p.Provider)
		}
		providers[p.Provider] = true
		ids := map[string]bool{}
		for _, m := range p.Models {
			for _, id := range append([]string{m.Model}, m.Aliases...) {
				if strings.TrimSpace(id) != id || id == "" || ids[id] {
					return fmt.Errorf("invalid or duplicate model/alias %q in %s", id, p.Provider)
				}
				ids[id] = true
			}
			u, err := url.Parse(m.SourceURL)
			if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
				return fmt.Errorf("%s/%s requires an HTTPS source_url", p.Provider, m.Model)
			}
			if _, err := time.Parse(time.DateOnly, m.VerifiedAt); err != nil {
				return fmt.Errorf("%s/%s verified_at: %w", p.Provider, m.Model, err)
			}
			if strings.TrimSpace(m.Notes) == "" {
				return fmt.Errorf("%s/%s requires pricing scope notes", p.Provider, m.Model)
			}
			for _, v := range []*float64{m.Input, m.Output, m.CacheRead, m.CacheWrite} {
				if v != nil && (*v < 0 || math.IsNaN(*v) || math.IsInf(*v, 0)) {
					return fmt.Errorf("%s/%s has an invalid price", p.Provider, m.Model)
				}
			}
		}
	}
	return nil
}

// LoadProviders produces a deterministic index from the editable provider files.
func LoadProviders(root fs.FS) (Catalog, error) {
	c := Catalog{Version: 1, Currency: "USD", Unit: "per_1m_tokens"}
	files, err := fs.Glob(root, "providers/*.json")
	if err != nil {
		return c, fmt.Errorf("list pricing providers: %w", err)
	}
	for _, name := range files {
		f, err := root.Open(name)
		if err != nil {
			return c, fmt.Errorf("open %s: %w", name, err)
		}
		var p Provider
		err = decode(f, &p)
		f.Close()
		if err != nil {
			return c, fmt.Errorf("%s: %w", name, err)
		}
		if name != "providers/"+p.Provider+".json" {
			return c, fmt.Errorf("provider %q does not match filename %s", p.Provider, name)
		}
		c.Providers = append(c.Providers, p)
	}
	return c, c.Validate()
}
