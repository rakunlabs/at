package service

import (
	"reflect"
	"testing"
)

func TestValidateDecisionQuestions(t *testing.T) {
	tests := []struct {
		name      string
		questions map[string]any
		wantErr   bool
	}{
		{name: "empty", questions: nil, wantErr: true},
		{name: "not object", questions: map[string]any{"q": "x"}, wantErr: true},
		{name: "unknown type", questions: map[string]any{"q": map[string]any{"type": "essay"}}, wantErr: true},
		{name: "choice without criteria", questions: map[string]any{"q": map[string]any{"type": "choice"}}, wantErr: true},
		{name: "noul", questions: map[string]any{"q": map[string]any{"type": "noul", "instructions": "?"}}},
		{name: "choice", questions: map[string]any{"q": map[string]any{"type": "choice", "criteria": map[string]any{"a": "x"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateDecisionQuestions(tt.questions); (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDecisionLowConfidence(t *testing.T) {
	answers := map[string]any{
		"a": map[string]any{"answer_confidence": 0.9, "confidence": 0.1},
		"b": map[string]any{"confidence": 0.4},
		"c": map[string]any{"low_confidence": true, "answer_confidence": 0.99},
		"d": map[string]any{"choice": "x"},
	}
	if got := DecisionLowConfidence(answers, 0); !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatalf("threshold off: %v", got)
	}
	if got := DecisionLowConfidence(answers, 0.5); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("threshold 0.5: %v", got)
	}
}
