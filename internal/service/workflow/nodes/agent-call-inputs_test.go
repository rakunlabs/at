package nodes

import (
	"reflect"
	"testing"
)

func TestInputStrings(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want []string
	}{
		{name: "nil", in: nil, want: []string{}},
		{name: "string trimmed", in: " http://a ", want: []string{"http://a"}},
		{name: "empty string dropped", in: "", want: []string{}},
		{name: "string slice", in: []string{"a", "", " b"}, want: []string{"a", "b"}},
		{name: "any slice skips non-strings", in: []any{"a", 3, "", "c"}, want: []string{"a", "c"}},
		{name: "unsupported type", in: 42, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inputStrings(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("inputStrings() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestUniqueStrings(t *testing.T) {
	got := uniqueStrings([]string{"b", "", "a", "b", "a", "c"})
	want := []string{"b", "a", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("uniqueStrings() = %#v, want %#v", got, want)
	}
	if uniqueStrings(nil) != nil {
		t.Fatal("uniqueStrings(nil) must be nil")
	}
}
