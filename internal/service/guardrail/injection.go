package guardrail

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// InjectionFinding names the heuristic that matched. It never carries the
// matched text, because findings are written to traces and error messages.
type InjectionFinding struct {
	Rule   string `json:"rule"`
	Strong bool   `json:"strong"`
}

type injectionRule struct {
	name   string
	strong bool
	re     *regexp.Regexp
}

// The rules cover English and Turkish phrasings of the common attack families:
// overriding prior instructions, role reassignment, system-prompt extraction,
// fake role/turn markers and exfiltration instructions. Strong rules are
// specific enough to act on alone; weak ones only count in strict mode.
var injectionRules = []injectionRule{
	{"ignore_instructions", true, regexp.MustCompile(`(?i)\b(ignore|disregard|forget|override|bypass|skip)\b.{0,40}\b(all |any |the |your |my )?(previous|prior|above|earlier|preceding|system|original|initial)\b.{0,30}\b(instructions?|prompts?|rules?|directions?|guidelines?|messages?|context)\b`)},
	{"ignore_instructions_tr", true, regexp.MustCompile(`(?i)(önceki|yukarıdaki|daha önceki|sistem|tüm|bütün)\s.{0,30}(talimat|komut|kural|yönerge|mesaj)\S*\s.{0,20}(unut|yok say|görmezden gel|dikkate alma|ihmal et|geçersiz)`)},
	{"new_instructions", true, regexp.MustCompile(`(?i)\b(new|updated|real|actual|true)\s+(system\s+)?(instructions?|rules?|prompt)\s*[:\-]`)},
	{"reveal_prompt", true, regexp.MustCompile(`(?i)\b(reveal|show|print|repeat|output|display|leak|tell me|give me|what (is|are))\b.{0,30}\b(your|the)\s+(system|hidden|initial|original|developer)\s+(prompt|instructions?|message|rules)\b`)},
	{"reveal_prompt_tr", true, regexp.MustCompile(`(?i)(sistem|gizli|ilk)\s+(prompt|talimat|mesaj|komut)\S*\s.{0,30}(göster|yaz|söyle|tekrarla|paylaş|ver)`)},
	{"role_marker", true, regexp.MustCompile(`(?im)(^|\n)\s*(<\|?(im_start|im_end|system|endoftext)\|?>|\[/?(INST|SYS)\]|<<SYS>>|###\s*(system|instruction)s?\s*:?)`)},
	{"fake_turn", true, regexp.MustCompile(`(?im)^\s*(system|assistant)\s*:\s*\S`)},
	{"developer_mode", true, regexp.MustCompile(`(?i)\b(developer|dan|god|jailbreak|unrestricted|sudo)\s+mode\b|\bdo anything now\b`)},
	{"exfiltrate", true, regexp.MustCompile(`(?i)\b(send|post|upload|forward|exfiltrate|transmit|leak)\b.{0,60}\b(api[_ ]?keys?|credentials?|passwords?|secrets?|tokens?|conversation|chat history|system prompt)\b.{0,40}\b(to|at|via)\b`)},
	{"markdown_exfil", true, regexp.MustCompile(`(?i)!\[[^\]]*\]\(https?://[^)\s]*[?&][a-z0-9_]+=(\{|\$|%7B|<)`)},
	{"role_reassign", false, regexp.MustCompile(`(?i)\b(you are now|from now on,? you|act as|pretend (to be|you are)|roleplay as|you will now)\b`)},
	{"role_reassign_tr", false, regexp.MustCompile(`(?i)(artık sen|bundan sonra sen|gibi davran|rolüne gir|rolünü oyna)`)},
	{"no_restrictions", false, regexp.MustCompile(`(?i)\b(without|no|ignore (all|any)?)\s+(restrictions|limitations|filters|guardrails|safety|censorship)\b`)},
	{"tool_directive", false, regexp.MustCompile(`(?i)\b(call|invoke|execute|run|use)\s+(the\s+)?(tool|function)\b.{0,40}\b(immediately|now|without asking|silently)\b`)},
	{"hidden_instruction", false, regexp.MustCompile(`(?i)\b(important|attention|note to (the )?(ai|assistant|model|llm))\s*[:!]`)},
}

// invisible characters used to hide instructions inside otherwise innocent
// text: zero-width spaces/joiners, bidi overrides and the Unicode tag block.
func invisible(r rune) bool {
	switch {
	case r == 0x200B || r == 0x200C || r == 0x200D || r == 0x2060 || r == 0xFEFF:
		return true
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
		return true
	case r >= 0xE0000 && r <= 0xE007F:
		return true
	}
	return false
}

// normalize folds compatibility forms (full-width letters, ligatures), drops
// invisible characters and collapses whitespace so obfuscation does not
// defeat the patterns. Turkish dotted/dotless I are folded by (?i) together
// with ToLower below.
func normalize(text string) (string, int) {
	text = norm.NFKC.String(text)
	hidden := 0
	var b strings.Builder
	b.Grow(len(text))
	space := false
	for _, r := range text {
		if invisible(r) {
			hidden++
			continue
		}
		if unicode.IsSpace(r) && r != '\n' {
			if !space {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return b.String(), hidden
}

// DetectInjection returns the heuristics text matches. In normal sensitivity
// only strong rules are reported; strict also reports weak rules.
func DetectInjection(text string, strict bool) []InjectionFinding {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	clean, hidden := normalize(text)
	var out []InjectionFinding
	if hidden >= 3 {
		out = append(out, InjectionFinding{Rule: "hidden_characters", Strong: strict})
	}
	for _, rule := range injectionRules {
		if !rule.strong && !strict {
			continue
		}
		if rule.re.MatchString(clean) {
			out = append(out, InjectionFinding{Rule: rule.name, Strong: rule.strong})
		}
	}
	if !strict {
		filtered := out[:0]
		for _, f := range out {
			if f.Strong {
				filtered = append(filtered, f)
			}
		}
		out = filtered
	}
	return out
}

// FindingRules lists the rule names of findings.
func FindingRules(findings []InjectionFinding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Rule)
	}
	return out
}
