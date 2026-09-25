package service

import "testing"

func TestValidateVariableUsePolicy(t *testing.T) {
	tests := []struct {
		name    string
		v       Variable
		wantErr bool
	}{
		{name: "disabled", v: Variable{}},
		{name: "exact host", v: Variable{AllowedTools: []string{VariableUseToolHTTPRequest}, AllowedHosts: []string{"api.example.com"}}},
		{name: "wildcard host", v: Variable{AllowedTools: []string{VariableUseToolHTTPRequest}, AllowedHosts: []string{"*.example.com"}}},
		{name: "tool needs host", v: Variable{AllowedTools: []string{VariableUseToolHTTPRequest}}, wantErr: true},
		{name: "host needs tool", v: Variable{AllowedHosts: []string{"api.example.com"}}, wantErr: true},
		{name: "URL is not host", v: Variable{AllowedTools: []string{VariableUseToolHTTPRequest}, AllowedHosts: []string{"https://api.example.com"}}, wantErr: true},
		{name: "broad wildcard", v: Variable{AllowedTools: []string{VariableUseToolHTTPRequest}, AllowedHosts: []string{"*"}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateVariableUsePolicy(tt.v)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateVariableUsePolicy() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestVariableAllowsHost(t *testing.T) {
	v := Variable{AllowedHosts: []string{"api.example.com", "*.service.example"}}
	tests := map[string]bool{
		"api.example.com":          true,
		"API.EXAMPLE.COM.":         true,
		"other.example.com":        false,
		"service.example":          false,
		"one.service.example":      true,
		"deep.one.service.example": true,
	}
	for host, want := range tests {
		if got := VariableAllowsHost(v, host); got != want {
			t.Errorf("VariableAllowsHost(%q) = %v, want %v", host, got, want)
		}
	}
}
