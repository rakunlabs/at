package skillmd

import "testing"

func TestGenerateForkExecutionMetadata(t *testing.T) {
	data, err := Generate(&SkillMD{
		Name: "review", Context: "fork", Agent: "reviewer", Background: true, Body: "Review carefully.",
	}, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	parsed, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Context != "fork" || parsed.Agent != "reviewer" || !parsed.Background {
		t.Fatalf("execution metadata did not round trip: %+v\n%s", parsed, data)
	}
}
