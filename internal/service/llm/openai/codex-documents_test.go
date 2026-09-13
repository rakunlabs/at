package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestCodexCanonicalDocumentInput(t *testing.T) {
	input := codexInput([]service.Message{{Role: "user", Content: []service.ContentBlock{
		{Type: "text", Text: "Read the spreadsheet"},
		{Type: "document", Source: &service.MediaSource{Type: "base64", Filename: "quarterly.xlsx", MediaType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", Data: "UEsDBA=="}},
	}}})
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type":"input_file"`, `"filename":"quarterly.xlsx"`, "UEsDBA==", "Read the spreadsheet"} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("document input missing %s: %s", want, encoded)
		}
	}
}
