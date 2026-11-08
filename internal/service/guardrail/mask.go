// Package guardrail holds the pure detection logic used by guardrail
// policies: sensitive-value masking, prompt-injection heuristics and the
// streaming hold-back used to restore placeholders in partial output.
package guardrail

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

// Detector finds one kind of sensitive value.
type Detector struct {
	Entity string
	re     *regexp.Regexp
	valid  func(string) bool
}

var builtinDetectors = map[string]Detector{
	"email":       {Entity: "email", re: regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`)},
	"phone":       {Entity: "phone", re: regexp.MustCompile(`(?:\+|\b00)?\(?\d{1,4}\)?(?:[ .\-]?\(?\d{2,4}\)?){2,5}\b`), valid: validPhone},
	"credit_card": {Entity: "credit_card", re: regexp.MustCompile(`\b(?:\d[ \-]?){12,18}\d\b`), valid: luhn},
	"iban":        {Entity: "iban", re: regexp.MustCompile(`(?i)\b[A-Z]{2}\d{2}(?:[ ]?[A-Z0-9]{2,4}){3,8}\b`), valid: validIBAN},
	"tr_id":       {Entity: "tr_id", re: regexp.MustCompile(`\b[1-9]\d{10}\b`), valid: validTCKN},
	"ipv4":        {Entity: "ipv4", re: regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`), valid: validIPv4},
	"api_key":     {Entity: "api_key", re: regexp.MustCompile(`\b(?:sk-(?:proj-|ant-)?[A-Za-z0-9_\-]{20,}|gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,}|AKIA[0-9A-Z]{16}|xox[abposr]-[A-Za-z0-9\-]{10,}|AIza[0-9A-Za-z_\-]{35}|glpat-[A-Za-z0-9_\-]{20,}|at_[0-9a-f]{64})\b`)},
	"jwt":         {Entity: "jwt", re: regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.eyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\b`)},
}

// detectorOrder makes overlapping matches deterministic: more specific
// entities claim a span before broader ones (a card number is not a phone).
var detectorOrder = []string{"jwt", "api_key", "email", "iban", "credit_card", "tr_id", "ipv4", "phone"}

// BuiltinDetector returns the detector for a built-in entity.
func BuiltinDetector(entity string) (Detector, bool) {
	d, ok := builtinDetectors[entity]
	return d, ok
}

// CustomDetector wraps an administrator-supplied pattern.
func CustomDetector(entity string, re *regexp.Regexp) Detector {
	return Detector{Entity: entity, re: re}
}

// processKey keeps placeholders stable for the life of the process, so a
// value masked in one call of a conversation gets the same placeholder in
// the next, even after history windowing shifts message positions.
var processKey = func() []byte {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return b
}()

// Masker replaces detected values with placeholders such as [EMAIL_3f2a91]
// and remembers the mapping so the response can be restored.
type Masker struct {
	detectors []Detector
	byValue   map[string]string
	byHolder  map[string]string
	counts    map[string]int
}

// NewMasker orders built-in detectors by specificity; custom ones run first.
func NewMasker(detectors []Detector) *Masker {
	rank := map[string]int{}
	for i, e := range detectorOrder {
		rank[e] = i + 1
	}
	sorted := append([]Detector(nil), detectors...)
	sort.SliceStable(sorted, func(i, j int) bool { return rank[sorted[i].Entity] < rank[sorted[j].Entity] })
	return &Masker{detectors: sorted, byValue: map[string]string{}, byHolder: map[string]string{}, counts: map[string]int{}}
}

// Active reports whether the masker has anything to detect.
func (m *Masker) Active() bool { return m != nil && len(m.detectors) > 0 }

// Counts reports how many distinct values of each entity were masked.
func (m *Masker) Counts() map[string]int {
	out := make(map[string]int, len(m.counts))
	for k, v := range m.counts {
		out[k] = v
	}
	return out
}

// Len reports how many distinct values were masked.
func (m *Masker) Len() int { return len(m.byValue) }

type span struct {
	start, end int
	entity     string
}

// Mask returns text with every detected value replaced.
func (m *Masker) Mask(text string) string {
	if !m.Active() || text == "" {
		return text
	}
	var spans []span
	taken := func(s, e int) bool {
		for _, sp := range spans {
			if s < sp.end && e > sp.start {
				return true
			}
		}
		return false
	}
	for _, d := range m.detectors {
		for _, loc := range d.re.FindAllStringIndex(text, -1) {
			s, e := loc[0], loc[1]
			if s == e || taken(s, e) {
				continue
			}
			if d.valid != nil && !d.valid(text[s:e]) {
				continue
			}
			spans = append(spans, span{s, e, d.Entity})
		}
	}
	if len(spans) == 0 {
		return text
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	var b strings.Builder
	last := 0
	for _, sp := range spans {
		b.WriteString(text[last:sp.start])
		b.WriteString(m.holder(sp.entity, text[sp.start:sp.end]))
		last = sp.end
	}
	b.WriteString(text[last:])
	return b.String()
}

func (m *Masker) holder(entity, value string) string {
	if h, ok := m.byValue[value]; ok {
		return h
	}
	mac := hmac.New(sha256.New, processKey)
	mac.Write([]byte(entity + "\x00" + value))
	sum := hex.EncodeToString(mac.Sum(nil))
	for n := 6; n <= len(sum); n += 2 {
		h := "[" + strings.ToUpper(entity) + "_" + sum[:n] + "]"
		if prev, clash := m.byHolder[h]; !clash || prev == value {
			m.byValue[value], m.byHolder[h] = h, value
			m.counts[entity]++
			return h
		}
	}
	return "[" + strings.ToUpper(entity) + "]"
}

var holderRe = regexp.MustCompile(`\[[A-Z][A-Z0-9_]*_[0-9a-f]{6,64}\]`)

// Restore puts original values back for every placeholder this masker issued.
// Unknown bracketed text is left untouched.
func (m *Masker) Restore(text string) string {
	if m == nil || len(m.byHolder) == 0 || !strings.Contains(text, "[") {
		return text
	}
	return holderRe.ReplaceAllStringFunc(text, func(h string) string {
		if v, ok := m.byHolder[h]; ok {
			return v
		}
		return h
	})
}

// RestoreValue restores placeholders inside a decoded JSON value (tool call
// arguments). It returns a copy; the input is not modified.
func (m *Masker) RestoreValue(v any) any {
	if m == nil || len(m.byHolder) == 0 {
		return v
	}
	switch t := v.(type) {
	case string:
		return m.Restore(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = m.RestoreValue(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = m.RestoreValue(x)
		}
		return out
	}
	return v
}

// MaxHolderLen bounds how much trailing text a stream must hold back while a
// placeholder may still be arriving.
const MaxHolderLen = 48

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func luhn(s string) bool {
	d := digits(s)
	if len(d) < 13 || len(d) > 19 {
		return false
	}
	sum, alt := 0, false
	for i := len(d) - 1; i >= 0; i-- {
		n := int(d[i] - '0')
		if alt {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		alt = !alt
	}
	return sum%10 == 0
}

func validPhone(s string) bool {
	d := digits(s)
	if len(d) < 10 || len(d) > 15 {
		return false
	}
	// A run of digits without any separator or + is more often an ID.
	return strings.ContainsAny(s, " .-()+") || strings.HasPrefix(d, "0")
}

func validIBAN(s string) bool {
	s = strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	if len(s) < 15 || len(s) > 34 {
		return false
	}
	r := s[4:] + s[:4]
	var b strings.Builder
	for _, c := range r {
		switch {
		case c >= '0' && c <= '9':
			b.WriteRune(c)
		case c >= 'A' && c <= 'Z':
			b.WriteString(big.NewInt(int64(c - 'A' + 10)).String())
		default:
			return false
		}
	}
	n, ok := new(big.Int).SetString(b.String(), 10)
	return ok && new(big.Int).Mod(n, big.NewInt(97)).Int64() == 1
}

// validTCKN checks the Turkish national ID checksum.
func validTCKN(s string) bool {
	if len(s) != 11 || s[0] == '0' {
		return false
	}
	d := make([]int, 11)
	for i := range s {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
		d[i] = int(s[i] - '0')
	}
	odd := d[0] + d[2] + d[4] + d[6] + d[8]
	even := d[1] + d[3] + d[5] + d[7]
	if ((odd*7-even)%10+10)%10 != d[9] {
		return false
	}
	sum := 0
	for i := 0; i < 10; i++ {
		sum += d[i]
	}
	return sum%10 == d[10]
}

func validIPv4(s string) bool {
	addr, err := netip.ParseAddr(s)
	return err == nil && addr.Is4() && !addr.IsUnspecified()
}
