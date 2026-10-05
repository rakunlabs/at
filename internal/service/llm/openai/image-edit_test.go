package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func TestCodexEditImage(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/codex/images/edits" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"created":1,"data":[{"b64_json":"aW1n"}]}`))
	}))
	defer server.Close()
	provider := NewCodexProvider("", "account", NewCodexTokenSource("token", "", "account", time.Time{}, nil),
		WithCodexBaseURL(server.URL+"/backend-api/codex/responses"),
		WithCodexHTTPClient(server.Client()),
	)
	resp, err := provider.GenerateImage(context.Background(), service.ImageGenerateRequest{
		Prompt:          "make it blue",
		ReferenceImages: []service.ReferenceImage{{Data: []byte("png"), ContentType: "image/png"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	images, _ := body["images"].([]any)
	first, _ := images[0].(map[string]any)
	if len(images) != 1 || first["image_url"] != "data:image/png;base64,cG5n" || body["prompt"] != "make it blue" {
		t.Fatalf("body = %#v", body)
	}
	if len(resp.Images) != 1 {
		t.Fatalf("images = %#v", resp.Images)
	}
}

func TestOpenAIEditImageMultipart(t *testing.T) {
	tests := []struct {
		name   string
		refs   int
		field  string
		images int
	}{
		{name: "single", refs: 1, field: "image", images: 1},
		{name: "several", refs: 2, field: "image[]", images: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotFiles int
			var form map[string][]string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/images/edits" || r.Header.Get("Authorization") != "Bearer token" {
					t.Errorf("request %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Fatal(err)
				}
				form = r.MultipartForm.Value
				for _, fh := range r.MultipartForm.File[tt.field] {
					f, _ := fh.Open()
					data, _ := io.ReadAll(f)
					f.Close()
					if string(data) == "png" && fh.Header.Get("Content-Type") == "image/png" {
						gotFiles++
					}
				}
				_, _ = w.Write([]byte(`{"data":[{"b64_json":"aW1n"}]}`))
			}))
			defer srv.Close()
			p, err := New("token", "model", srv.URL+"/v1", "", false, nil)
			if err != nil {
				t.Fatal(err)
			}
			refs := make([]service.ReferenceImage, tt.refs)
			for i := range refs {
				refs[i] = service.ReferenceImage{Data: []byte("png"), ContentType: "image/png"}
			}
			resp, err := p.GenerateImage(context.Background(), service.ImageGenerateRequest{Prompt: "edit", Model: "gpt-image-1.5", Background: "transparent", ReferenceImages: refs})
			if err != nil {
				t.Fatal(err)
			}
			if gotFiles != tt.images || form["prompt"][0] != "edit" || form["model"][0] != "gpt-image-1.5" || form["background"][0] != "transparent" {
				t.Fatalf("files=%d form=%v", gotFiles, form)
			}
			if _, ok := form["response_format"]; ok {
				t.Fatal("GPT Image models reject response_format")
			}
			if len(resp.Images) != 1 || resp.Images[0].Base64 != "aW1n" {
				t.Fatalf("images = %#v", resp.Images)
			}
		})
	}
}
