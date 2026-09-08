package common

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/ok"
)

func TestWithDefaultHeaders(t *testing.T) {
	for _, value := range []string{"absent", "", "Bearer fresh"} {
		t.Run(value, func(t *testing.T) {
			want := value
			if value == "absent" {
				want = "Bearer default"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != want {
					t.Errorf("Authorization = %q, want %q", got, want)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			client, err := ok.New(ok.WithDisableRetry(true), WithDefaultHeaders(http.Header{
				"Authorization": {"Bearer default"},
			}))
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			if value != "absent" {
				req.Header.Set("Authorization", value)
			}
			if err := client.Do(req, func(*http.Response) error { return nil }); err != nil {
				t.Fatal(err)
			}
		})
	}
}
