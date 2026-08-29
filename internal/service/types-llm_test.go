package service

import (
	"errors"
	"strings"
	"testing"
)

func TestUpstreamError(t *testing.T) {
	underlying := errors.New("wire failure")
	err := &UpstreamError{
		Provider:   "example",
		StatusCode: 503,
		Code:       "overloaded",
		Param:      "model",
		Message:    "try later",
		Underlying: underlying,
	}

	for _, want := range []string{"example", "503", "overloaded", "model", "try later"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Error() = %q, want it to contain %q", err.Error(), want)
		}
	}
	if !errors.Is(err, underlying) {
		t.Fatal("UpstreamError does not unwrap its underlying error")
	}
}
