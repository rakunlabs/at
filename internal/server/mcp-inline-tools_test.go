package server

import (
	"reflect"
	"testing"
)

func TestInlineToolVariableKeys(t *testing.T) {
	handler := `
var token = getVar('pexels_api_key');
API_KEY="${VAR_FAL_API_KEY:-${VAR_FAL_KEY:-}}"
var again = getVar("pexels_api_key");
`
	want := []string{"fal_api_key", "fal_key", "pexels_api_key"}
	if got := inlineToolVariableKeys(handler); !reflect.DeepEqual(got, want) {
		t.Fatalf("inlineToolVariableKeys() = %#v, want %#v", got, want)
	}
}
