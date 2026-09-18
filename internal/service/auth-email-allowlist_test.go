package service

import (
	"errors"
	"strings"
	"testing"
)

func TestAuthEmailAdmits(t *testing.T) {
	list := []string{"Ada@Example.com", "@firma.test"}
	tests := []struct {
		name     string
		list     []string
		email    string
		verified bool
		want     error
	}{
		// No list is the shipped behaviour and must stay unconditional, including
		// for a provider that reports no email at all.
		{"empty list admits anyone", nil, "anyone@elsewhere.test", true, nil},
		{"empty list admits a provider with no email", nil, "", false, nil},

		{"exact address", list, "ada@example.com", true, nil},
		{"exact address ignores case on both sides", list, "ADA@Example.COM", true, nil},
		{"exact address ignores surrounding space", list, "  ada@example.com ", true, nil},
		{"domain entry", list, "someone@firma.test", true, nil},
		{"domain entry ignores case", list, "Someone@FIRMA.test", true, nil},

		{"other address at a listed domain's sibling", list, "ada@example.org", true, ErrAuthEmailNotAllowed},
		{"other address at the exact entry's domain", list, "eve@example.com", true, ErrAuthEmailNotAllowed},
		// A domain rule that quietly covered subdomains would extend to every
		// name the provider's operator can create under it.
		{"subdomain is not the domain", list, "someone@mail.firma.test", true, ErrAuthEmailNotAllowed},
		{"domain is not a suffix match", list, "someone@notfirma.test", true, ErrAuthEmailNotAllowed},

		// The allowlist's whole value is in what it refuses, so an address the
		// provider will hand to whoever claims it is not an identity.
		{"unverified address", list, "ada@example.com", false, ErrAuthEmailUnverified},
		{"absent address", list, "", false, ErrAuthEmailUnverified},
		{"absent address reported verified", list, "", true, ErrAuthEmailUnverified},

		// A malformed address must not derive a domain that matches an entry.
		{"empty local part", list, "@firma.test", true, ErrAuthEmailNotAllowed},
		{"no at sign", list, "firma.test", true, ErrAuthEmailNotAllowed},
		{"empty domain", list, "ada@", true, ErrAuthEmailNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := AuthEmailAdmits(tt.list, tt.email, tt.verified); !errors.Is(err, tt.want) {
				t.Fatalf("AuthEmailAdmits(%v, %q, %v) = %v, want %v", tt.list, tt.email, tt.verified, err, tt.want)
			}
		})
	}
}

func TestNormalizeAuthEmailAllowlist(t *testing.T) {
	got := NormalizeAuthEmailAllowlist([]string{" Ada@Example.com ", "ada@example.com", "", "   ", "@Firma.test"})
	want := []string{"ada@example.com", "@firma.test"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if NormalizeAuthEmailAllowlist(nil) != nil || NormalizeAuthEmailAllowlist([]string{"", " "}) != nil {
		t.Fatal("a list with nothing in it must normalize to nil, which admits everyone")
	}
}

func TestValidateAuthEmailAllowlist(t *testing.T) {
	for _, list := range [][]string{
		nil,
		{"ada@example.com"},
		{"@example.com"},
		{"ada@example.com", "@firma.test", "Grace.Hopper+at@sub.example.co.uk"},
		// Normalization runs first, so validation accepts what saving stores.
		{" ADA@EXAMPLE.COM ", "ada@example.com"},
		{"üye@şirket.test"},
	} {
		if err := ValidateAuthEmailAllowlist(list); err != nil {
			t.Errorf("rejected %v: %v", list, err)
		}
	}
	for _, list := range [][]string{
		{"ada"},
		{"@"},
		{"ada@"},
		{"ada@@example.com"},
		{"ada example@example.com"},
		{"ada@.example.com"},
		{"ada@example.com."},
		// A wildcard would otherwise validate as an address with the local part
		// `*` and silently match nobody.
		{"*@example.com"},
		{"*"},
		{strings.Repeat("a", 320) + "@example.com"},
	} {
		if err := ValidateAuthEmailAllowlist(list); err == nil {
			t.Errorf("accepted %v", list)
		}
	}
	oversized := make([]string, 0, AuthEmailAllowlistMax+1)
	for i := 0; i <= AuthEmailAllowlistMax; i++ {
		oversized = append(oversized, string(rune('a'+i%26))+string(rune('a'+i/26))+"@example.com")
	}
	if err := ValidateAuthEmailAllowlist(oversized); err == nil {
		t.Fatalf("accepted %d entries", len(oversized))
	}
}
