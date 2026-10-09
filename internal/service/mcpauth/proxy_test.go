package mcpauth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestParseProxy(t *testing.T) {
	for _, raw := range []string{"", " http://127.0.0.1:8080 ", "https://proxy.example", "socks5://proxy.example:1080", "socks5h://proxy.example:1080"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseProxy(raw); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, raw := range []string{"proxy:8080", "ftp://proxy.example", "http://", "http://user:password@proxy.example", "http://proxy.example/path", "http://proxy.example?x=1", "http://proxy.example#x", "http://proxy.example:0", "http://proxy.example:65536", "http://proxy.example:"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseProxy(raw); err == nil {
				t.Fatal("invalid proxy accepted")
			}
		})
	}
}

func TestOAuthExplicitProxy(t *testing.T) {
	var paths []string
	var mu sync.Mutex
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.IsAbs() || r.URL.Host != "10.23.45.67" {
			t.Errorf("not a forward proxy request: %s", r.URL)
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") == "" {
				t.Error("missing token form")
			}
			_, _ = io.WriteString(w, `{"access_token":"ok","token_type":"Bearer"}`)
		case "/redirect":
			w.Header().Set("Location", "http://10.23.45.67/token")
			w.WriteHeader(http.StatusTemporaryRedirect)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	client, err := ClientWithProxy(true, proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if _, err := Exchange(t.Context(), client, "http://10.23.45.67/token", "client", "", "code", "https://at.example/cb", "verifier", "resource"); err != nil {
		t.Fatal(err)
	}
	if _, err := Refresh(t.Context(), client, "http://10.23.45.67/token", "client", "", "refresh", "resource"); err != nil {
		t.Fatal(err)
	}
	if _, err := Refresh(t.Context(), client, "http://10.23.45.67/redirect", "client", "", "refresh", "resource"); err == nil {
		t.Fatal("token POST redirect must still be refused")
	}
	mu.Lock()
	if len(paths) != 3 {
		t.Fatalf("proxy requests: %v", paths)
	}
	mu.Unlock()
	public, err := ClientWithProxy(false, proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer public.CloseIdleConnections()
	if _, err := Refresh(t.Context(), public, "http://10.23.45.67/token", "client", "", "refresh", "resource"); err == nil {
		t.Fatal("proxy must not disable HTTPS endpoint requirements")
	}
}

func TestOAuthTLSVerificationOptIn(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"ok","token_type":"Bearer"}`)
	}))
	defer srv.Close()
	for _, insecure := range []bool{false, true} {
		client, err := ClientWithTransport(true, "", insecure)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Refresh(t.Context(), client, srv.URL, "client", "", "refresh", "resource")
		client.CloseIdleConnections()
		if (err == nil) != insecure {
			t.Fatalf("insecure=%v: %v", insecure, err)
		}
	}
}
