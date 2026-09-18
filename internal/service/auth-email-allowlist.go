package service

import (
	"errors"
	"fmt"
	"strings"
)

// An identity provider's allowed-email list is the answer to "which people at
// this provider may sign in here". Without one, any successful ceremony against
// an enabled provider provisions an account, which is correct for an IdP that
// already only holds the right people and wrong for a public one (Google,
// GitHub, Microsoft consumer accounts), where the provider admits the world.
//
// An empty list keeps exactly the previous behaviour, so the feature is opt-in
// and no installation changes on upgrade.
const (
	// AuthEmailAllowlistMax bounds the stored list. This is installation
	// configuration edited in a textarea, not a user directory: past a couple
	// of hundred people the right expression is a domain entry or a workspace
	// permission mapping on a provider-asserted group claim.
	AuthEmailAllowlistMax = 256
	// authEmailEntryMax is RFC 5321's maximum reverse-path length.
	authEmailEntryMax = 320
)

var (
	// ErrAuthEmailUnverified is refusal because there is nothing to match. It is
	// deliberately distinct from ErrAuthEmailNotAllowed: an administrator who
	// configures a list against a provider that reports no verified email sees
	// every sign-in fail, and "not on the list" would send them looking for a
	// typo that is not there.
	ErrAuthEmailUnverified = errors.New("this identity provider did not report a verified email address, so the allowed-address list cannot admit it")
	// ErrAuthEmailNotAllowed is refusal by the list itself.
	ErrAuthEmailNotAllowed = errors.New("this address is not on the identity provider's allowed list")
)

// NormalizeAuthEmailAllowlist canonicalizes a stored list: trimmed, lowercased,
// blanks dropped, order-preserving deduplication.
//
// Lowercasing both sides is a deliberate deviation from RFC 5321, which makes
// the local part case-sensitive. No identity provider in practice treats
// Ali@example.com and ali@example.com as two people, while an administrator
// typing the address with different capitalization than the provider reports it
// is entirely ordinary — and here that mismatch is a lockout, not a cosmetic
// difference.
func NormalizeAuthEmailAllowlist(entries []string) []string {
	if len(entries) == 0 {
		return nil
	}
	out := make([]string, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		value := strings.ToLower(strings.TrimSpace(entry))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ValidateAuthEmailAllowlist bounds and checks the configured list. It
// normalizes first, so validation accepts exactly what normalization stores and
// a list cannot be refused for something saving would have fixed.
func ValidateAuthEmailAllowlist(entries []string) error {
	allowed := NormalizeAuthEmailAllowlist(entries)
	if len(allowed) > AuthEmailAllowlistMax {
		return fmt.Errorf("at most %d allowed email entries", AuthEmailAllowlistMax)
	}
	for _, entry := range allowed {
		if err := validateAuthEmailEntry(entry); err != nil {
			return err
		}
	}
	return nil
}

func validateAuthEmailEntry(entry string) error {
	if len(entry) > authEmailEntryMax {
		return fmt.Errorf("allowed email entry exceeds %d bytes", authEmailEntryMax)
	}
	if strings.ContainsFunc(entry, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return fmt.Errorf("allowed email %q contains whitespace or control characters", entry)
	}
	// A wildcard is the shape an administrator reaches for first and would be
	// accepted as an ordinary address with the local part `*`, matching nobody.
	// Silently admitting a rule that can never fire is the one outcome worse
	// than refusing it.
	if strings.Contains(entry, "*") {
		return fmt.Errorf("allowed email %q must not use a wildcard; write @example.com to allow a whole domain", entry)
	}
	at := strings.Index(entry, "@")
	if at < 0 || at != strings.LastIndex(entry, "@") {
		return fmt.Errorf("allowed email %q must be an address (person@example.com) or a domain (@example.com)", entry)
	}
	domain := entry[at+1:]
	if domain == "" || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return fmt.Errorf("allowed email %q has no usable domain", entry)
	}
	return nil
}

// AuthEmailAdmits reports whether a provider's allowed-email list admits an
// identity. An empty list admits everything.
//
// A non-empty list requires a *verified* address. The external strategy runs
// with EmailVerifyCheck, so an address the provider did not assert
// `email_verified: true` for never reaches AT at all — but the check is written
// against both inputs rather than relying on that, because the value of an
// allowlist is entirely in what it refuses. An address a provider will hand to
// whoever claims it is not an identity, and accepting one would let anybody who
// can type a listed address into a public IdP's unverified profile walk in.
//
// A domain entry matches that domain exactly. Subdomains are not matched:
// @example.com admitting mail.example.com would silently widen the rule to
// every subdomain the provider's operator can create.
func AuthEmailAdmits(allowlist []string, email string, verified bool) error {
	allowed := NormalizeAuthEmailAllowlist(allowlist)
	if len(allowed) == 0 {
		return nil
	}
	if email == "" || !verified {
		return ErrAuthEmailUnverified
	}
	value := strings.ToLower(strings.TrimSpace(email))
	// Reject a malformed address before deriving a domain from it: "@example.com"
	// has no local part, and its derived domain would match a domain entry.
	at := strings.LastIndex(value, "@")
	if at <= 0 || at == len(value)-1 {
		return ErrAuthEmailNotAllowed
	}
	// Domain entries are stored with their leading "@", so one comparison
	// covers both forms.
	domain := value[at:]
	for _, entry := range allowed {
		if entry == value || entry == domain {
			return nil
		}
	}
	return ErrAuthEmailNotAllowed
}
