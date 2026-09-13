package pricing

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestPublishedCatalogMatchesProviders(t *testing.T) {
	want, err := LoadProviders(os.DirFS("."))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open("index.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("index.json is stale: run go run ./pricing/cmd/catalog from the repository root")
	}
}

const validCatalog = `{"version":1,"currency":"USD","unit":"per_1m_tokens","providers":[{"provider":"example","models":[{"model":"free","source_url":"https://example.com/pricing","verified_at":"2026-09-13","notes":"Explicitly free model","input":0,"output":0,"cache_read":0,"cache_write":0}]}]}`

func TestCatalogValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		edit func(*Catalog)
	}{
		{"version", func(c *Catalog) { c.Version = 2 }},
		{"currency", func(c *Catalog) { c.Currency = "EUR" }},
		{"unit", func(c *Catalog) { c.Unit = "per_token" }},
		{"empty", func(c *Catalog) { c.Providers = nil }},
		{"duplicate provider", func(c *Catalog) { c.Providers = append(c.Providers, c.Providers[0]) }},
		{"duplicate model", func(c *Catalog) { c.Providers[0].Models = append(c.Providers[0].Models, c.Providers[0].Models[0]) }},
		{"alias collision", func(c *Catalog) { c.Providers[0].Models[0].Aliases = []string{"free"} }},
		{"negative", func(c *Catalog) { *c.Providers[0].Models[0].Input = -1 }},
		{"source", func(c *Catalog) { c.Providers[0].Models[0].SourceURL = "http://example.com" }},
		{"date", func(c *Catalog) { c.Providers[0].Models[0].VerifiedAt = "2026-02-30" }},
		{"notes", func(c *Catalog) { c.Providers[0].Models[0].Notes = "" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Parse(strings.NewReader(validCatalog))
			if err != nil {
				t.Fatal(err)
			}
			tt.edit(&c)
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Parse(strings.NewReader(string(data))); err == nil {
				t.Fatal("expected invalid catalog to fail")
			}
		})
	}
	for _, body := range []string{validCatalog + `{}`, strings.Replace(validCatalog, `"version":1`, `"typo":1`, 1)} {
		if _, err := Parse(strings.NewReader(body)); err == nil {
			t.Fatal("expected invalid JSON document to fail")
		}
	}
}

func TestUnknownAndFreePrices(t *testing.T) {
	c, err := Parse(strings.NewReader(validCatalog))
	if err != nil || !c.Providers[0].Models[0].Complete() {
		t.Fatalf("free prices must be usable: %+v, %v", c, err)
	}
	c, err = Parse(strings.NewReader(strings.Replace(validCatalog, `"cache_read":0`, `"cache_read":null`, 1)))
	if err != nil || c.Providers[0].Models[0].Complete() {
		t.Fatalf("unknown prices must stay incomplete: %+v, %v", c, err)
	}
}
