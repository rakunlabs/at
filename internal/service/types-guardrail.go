package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Guardrail policies inspect model calls before they reach a provider and the
// responses before they reach the caller. They are applied at the provider
// boundary (budgetedProvider), so the gateway, Chats, Sessions, organization
// delegation and workflow nodes are all covered by one implementation.
const (
	GuardrailModeEnforce = "enforce"
	GuardrailModeMonitor = "monitor"

	GuardrailActionBlock    = "block"
	GuardrailActionWarn     = "warn"
	GuardrailActionWithhold = "withhold"
	GuardrailActionRedact   = "redact"
	GuardrailActionMask     = "mask"
	GuardrailActionOff      = "off"

	GuardrailCheckInput      = "input_filter"
	GuardrailCheckInjection  = "injection"
	GuardrailCheckToolInject = "tool_injection"
	GuardrailCheckMasking    = "masking"
	GuardrailCheckOutput     = "output_filter"
	GuardrailCheckClassifier = "classifier"
)

// GuardrailEntities are the built-in detectors masking can use.
var GuardrailEntities = []string{"email", "phone", "credit_card", "iban", "tr_id", "ipv4", "api_key", "jwt"}

var (
	ErrGuardrailPolicyNotFound = errors.New("guardrail policy not found")
	ErrGuardrailInvalid        = errors.New("invalid guardrail policy")
	// ErrGuardrailBlocked is wrapped by every GuardrailError.
	ErrGuardrailBlocked = errors.New("request blocked by guardrail")
)

// GuardrailError reports which policy and check refused a call. The message
// names the check, never the matched content.
type GuardrailError struct {
	PolicyID   string
	PolicyName string
	Check      string
	Message    string
}

func (e *GuardrailError) Error() string {
	name := e.PolicyName
	if name == "" {
		name = e.PolicyID
	}
	return fmt.Sprintf("blocked by guardrail %q (%s): %s", name, e.Check, e.Message)
}

func (e *GuardrailError) Unwrap() error { return ErrGuardrailBlocked }

// GuardrailPattern is a named regular expression (RE2 syntax).
type GuardrailPattern struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
}

// GuardrailInputFilter checks the text of the new turn.
type GuardrailInputFilter struct {
	// MaxChars refuses a new turn longer than this; 0 = no limit.
	MaxChars int                `json:"max_chars,omitempty"`
	Deny     []GuardrailPattern `json:"deny,omitempty"`
}

// GuardrailInjection detects prompt injection in the user's new turn and in
// tool results (where content from Jira, Confluence, web pages arrives).
type GuardrailInjection struct {
	Enabled bool `json:"enabled"`
	// Action for user text: block | warn.
	Action string `json:"action,omitempty"`
	// ToolResults: off | warn | withhold | block. Withhold replaces the
	// suspicious tool result with a notice and lets the call continue.
	ToolResults string `json:"tool_results,omitempty"`
	// Sensitivity: normal (high-confidence patterns) | strict (also weaker ones).
	Sensitivity string `json:"sensitivity,omitempty"`
	// Classifier is an optional "provider/model" asked to classify text the
	// patterns did not flag. Empty = patterns only.
	Classifier string `json:"classifier,omitempty"`
	// FailClosed blocks when the classifier cannot be reached.
	FailClosed bool `json:"fail_closed,omitempty"`
}

// GuardrailMasking replaces sensitive values with stable placeholders before
// the provider sees them.
type GuardrailMasking struct {
	Entities []string           `json:"entities,omitempty"`
	Custom   []GuardrailPattern `json:"custom,omitempty"`
	// Restore puts the original values back into the response (text and tool
	// call arguments), so the caller and tools see real values while the
	// provider only sees placeholders.
	Restore bool `json:"restore,omitempty"`
}

// GuardrailOutputFilter checks model output.
type GuardrailOutputFilter struct {
	Deny []GuardrailPattern `json:"deny,omitempty"`
	// Action: block | redact.
	Action string `json:"action,omitempty"`
}

type GuardrailConfig struct {
	Input     GuardrailInputFilter  `json:"input"`
	Injection GuardrailInjection    `json:"injection"`
	Masking   GuardrailMasking      `json:"masking"`
	Output    GuardrailOutputFilter `json:"output"`
}

// GuardrailPolicy matches calls on server-stamped attributes (as trace
// privacy rules do). Empty fields match everything; set fields are ANDed.
type GuardrailPolicy struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspace_id"`
	Scope       string          `json:"scope"` // installation | workspace (derived)
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Enabled     bool            `json:"enabled"`
	Mode        string          `json:"mode"`
	UserID      string          `json:"user_id"`
	TokenID     string          `json:"token_id"`
	Provider    string          `json:"provider"`
	Model       string          `json:"model"` // glob; with "/" matched against provider/model
	Source      string          `json:"source"`
	Config      GuardrailConfig `json:"config"`
	CreatedBy   string          `json:"created_by"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

// GuardrailFacts are the attributes a policy may match on.
type GuardrailFacts struct {
	WorkspaceID string
	UserID      string
	TokenID     string
	Provider    string
	Model       string
	Source      string
}

// GuardrailStorer persists policies. LoadGuardrailPolicies is an internal
// lookup for enforcement; the other methods revalidate the caller.
type GuardrailStorer interface {
	LoadGuardrailPolicies(ctx context.Context) ([]GuardrailPolicy, error)
	ListGuardrailPolicies(ctx context.Context) ([]GuardrailPolicy, error)
	SaveGuardrailPolicy(ctx context.Context, policy GuardrailPolicy) (*GuardrailPolicy, error)
	DeleteGuardrailPolicy(ctx context.Context, id string) error
}

const (
	guardrailMaxPatterns = 50
	guardrailMaxPattern  = 1000
)

func normalizeGuardrailPatterns(field string, in []GuardrailPattern) ([]GuardrailPattern, error) {
	if len(in) > guardrailMaxPatterns {
		return nil, fmt.Errorf("%w: %s allows at most %d patterns", ErrGuardrailInvalid, field, guardrailMaxPatterns)
	}
	out := make([]GuardrailPattern, 0, len(in))
	for i, p := range in {
		p.Name = strings.TrimSpace(p.Name)
		p.Pattern = strings.TrimSpace(p.Pattern)
		if p.Pattern == "" {
			continue
		}
		if len(p.Pattern) > guardrailMaxPattern || len(p.Name) > 64 {
			return nil, fmt.Errorf("%w: %s pattern %d is too long", ErrGuardrailInvalid, field, i+1)
		}
		if _, err := regexp.Compile(p.Pattern); err != nil {
			return nil, fmt.Errorf("%w: %s pattern %q: %v", ErrGuardrailInvalid, field, p.Name, err)
		}
		if p.Name == "" {
			p.Name = fmt.Sprintf("pattern_%d", i+1)
		}
		out = append(out, p)
	}
	return out, nil
}

func oneOf(v, def string, allowed ...string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return def, true
	}
	for _, a := range allowed {
		if v == a {
			return v, true
		}
	}
	return v, false
}

// NormalizeGuardrailPolicy trims fields, applies defaults and validates.
func NormalizeGuardrailPolicy(p GuardrailPolicy) (GuardrailPolicy, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	if p.Name == "" || len(p.Name) > 128 || !utf8.ValidString(p.Name) {
		return p, fmt.Errorf("%w: name is required (at most 128 bytes)", ErrGuardrailInvalid)
	}
	if len(p.Description) > 512 || !utf8.ValidString(p.Description) {
		return p, fmt.Errorf("%w: description must be at most 512 bytes", ErrGuardrailInvalid)
	}
	var ok bool
	if p.Mode, ok = oneOf(p.Mode, GuardrailModeEnforce, GuardrailModeEnforce, GuardrailModeMonitor); !ok {
		return p, fmt.Errorf("%w: mode must be enforce or monitor", ErrGuardrailInvalid)
	}
	for name, v := range map[string]*string{"user_id": &p.UserID, "token_id": &p.TokenID, "provider": &p.Provider, "model": &p.Model, "source": &p.Source} {
		*v = strings.TrimSpace(*v)
		if len(*v) > 256 || !utf8.ValidString(*v) || strings.IndexFunc(*v, unicode.IsControl) >= 0 {
			return p, fmt.Errorf("%w: %s must be at most 256 printable characters", ErrGuardrailInvalid, name)
		}
	}

	c := &p.Config
	var err error
	if c.Input.MaxChars < 0 {
		return p, fmt.Errorf("%w: input.max_chars must not be negative", ErrGuardrailInvalid)
	}
	if c.Input.Deny, err = normalizeGuardrailPatterns("input.deny", c.Input.Deny); err != nil {
		return p, err
	}
	if c.Injection.Action, ok = oneOf(c.Injection.Action, GuardrailActionBlock, GuardrailActionBlock, GuardrailActionWarn); !ok {
		return p, fmt.Errorf("%w: injection.action must be block or warn", ErrGuardrailInvalid)
	}
	if c.Injection.ToolResults, ok = oneOf(c.Injection.ToolResults, GuardrailActionWithhold, GuardrailActionOff, GuardrailActionWarn, GuardrailActionWithhold, GuardrailActionBlock); !ok {
		return p, fmt.Errorf("%w: injection.tool_results must be off, warn, withhold or block", ErrGuardrailInvalid)
	}
	if c.Injection.Sensitivity, ok = oneOf(c.Injection.Sensitivity, "normal", "normal", "strict"); !ok {
		return p, fmt.Errorf("%w: injection.sensitivity must be normal or strict", ErrGuardrailInvalid)
	}
	c.Injection.Classifier = strings.TrimSpace(c.Injection.Classifier)
	if c.Injection.Classifier != "" && !strings.Contains(c.Injection.Classifier, "/") {
		return p, fmt.Errorf("%w: injection.classifier must be provider/model", ErrGuardrailInvalid)
	}
	seen := map[string]bool{}
	entities := make([]string, 0, len(c.Masking.Entities))
	for _, e := range c.Masking.Entities {
		e = strings.TrimSpace(e)
		if e == "" || seen[e] {
			continue
		}
		known := false
		for _, k := range GuardrailEntities {
			known = known || k == e
		}
		if !known {
			return p, fmt.Errorf("%w: unknown masking entity %q", ErrGuardrailInvalid, e)
		}
		seen[e] = true
		entities = append(entities, e)
	}
	c.Masking.Entities = entities
	if c.Masking.Custom, err = normalizeGuardrailPatterns("masking.custom", c.Masking.Custom); err != nil {
		return p, err
	}
	for _, cp := range c.Masking.Custom {
		if !guardrailEntityName.MatchString(cp.Name) {
			return p, fmt.Errorf("%w: custom masking name %q must be letters, digits or _", ErrGuardrailInvalid, cp.Name)
		}
	}
	if c.Output.Deny, err = normalizeGuardrailPatterns("output.deny", c.Output.Deny); err != nil {
		return p, err
	}
	if c.Output.Action, ok = oneOf(c.Output.Action, GuardrailActionBlock, GuardrailActionBlock, GuardrailActionRedact); !ok {
		return p, fmt.Errorf("%w: output.action must be block or redact", ErrGuardrailInvalid)
	}

	if p.WorkspaceID == "" {
		p.Scope = "installation"
	} else {
		p.Scope = "workspace"
	}
	return p, nil
}

var guardrailEntityName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}$`)

// Matches reports whether the policy applies to a call.
func (p GuardrailPolicy) Matches(f GuardrailFacts) bool {
	if !p.Enabled {
		return false
	}
	if p.WorkspaceID != "" && p.WorkspaceID != f.WorkspaceID {
		return false
	}
	if p.UserID != "" && p.UserID != f.UserID {
		return false
	}
	if p.TokenID != "" && p.TokenID != f.TokenID {
		return false
	}
	if p.Provider != "" && p.Provider != f.Provider {
		return false
	}
	if p.Source != "" && p.Source != f.Source {
		return false
	}
	if p.Model != "" {
		subject := f.Model
		if strings.Contains(p.Model, "/") {
			subject = f.Provider + "/" + f.Model
		}
		if f.Model == "" || !GlobMatch(strings.ToLower(p.Model), strings.ToLower(subject)) {
			return false
		}
	}
	return true
}
