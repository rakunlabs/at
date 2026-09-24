package service

import "testing"

func TestValidateSkillExecution(t *testing.T) {
	tests := []struct {
		name    string
		skill   Skill
		wantErr bool
	}{
		{name: "inline", skill: Skill{}},
		{name: "foreground fork", skill: Skill{Context: "fork", Agent: "reviewer"}},
		{name: "background fork", skill: Skill{Context: "fork", Agent: "reviewer", Background: true}},
		{name: "missing agent", skill: Skill{Context: "fork"}, wantErr: true},
		{name: "agent without fork", skill: Skill{Agent: "reviewer"}, wantErr: true},
		{name: "unknown context", skill: Skill{Context: "thread", Agent: "reviewer"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSkillExecution(tt.skill)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateSkillExecution() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
