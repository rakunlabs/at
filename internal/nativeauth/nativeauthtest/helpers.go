package nativeauthtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/config"
)

func Request(handler http.Handler, method, target, body, origin string, c *http.Cookie, opts ...func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if c != nil {
		r.AddCookie(c)
	}
	for _, opt := range opts {
		opt(r)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func Config() config.Server {
	return config.Server{BasePath: "/at", NativeAuth: &config.NativeAuth{Enabled: true, Origin: "https://at.example", BootstrapToken: strings.Repeat("t", 32)}}
}

func LoginCookie(t *testing.T, mux http.Handler, user string) *http.Cookie {
	t.Helper()
	w := Request(mux, "POST", "/at/auth/login", `{"username":"`+user+`","password":"`+strings.Repeat("x", 32)+`"}`, "https://at.example", nil)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cookies: %+v", cookies)
	}
	c := cookies[0]
	if !c.HttpOnly || !c.Secure || c.Path != "/at/" || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 0 || !c.Expires.IsZero() || c.Domain != "" {
		t.Fatalf("unsafe cookie: %+v", c)
	}
	if strings.Contains(w.Body.String(), c.Value) || strings.Contains(w.Body.String(), "session_version") || strings.Contains(w.Body.String(), "password") {
		t.Fatalf("credential leak: %s", w.Body)
	}
	return c
}

func PasskeyRequest(h http.Handler, path, body, origin string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", origin)
	for _, c := range cookies {
		if c != nil {
			r.AddCookie(c)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func PasskeyBegin(t *testing.T, h http.Handler, enroll bool, session *http.Cookie, remember bool) (string, *http.Cookie) {
	t.Helper()
	path, body := "/at/auth/passkeys/login/begin", fmt.Sprintf(`{"username":"reader","remember_me":%t}`, remember)
	if enroll {
		path, body = "/at/auth/passkeys/enroll/begin", `{"name":"Laptop","current_password":"`+strings.Repeat("x", 32)+`"}`
	}
	w := PasskeyRequest(h, path, body, "https://at.example", session)
	if w.Code != 200 {
		t.Fatalf("begin: %d %s", w.Code, w.Body)
	}
	var result struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			RP        struct {
				ID string `json:"id"`
			} `json:"rp"`
			RPID string `json:"rpId"`
			User struct {
				ID string `json:"id"`
			} `json:"user"`
			Selection struct {
				UV       string `json:"userVerification"`
				Resident string `json:"residentKey"`
			} `json:"authenticatorSelection"`
			UV string `json:"userVerification"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if enroll && (result.PublicKey.RP.ID != "at.example" || result.PublicKey.Selection.UV != "required" || result.PublicKey.Selection.Resident != "preferred") {
		t.Fatal("unsafe enrollment options")
	}
	if !enroll && (result.PublicKey.RPID != "at.example" || result.PublicKey.UV != "required") {
		t.Fatal("unsafe login options")
	}
	c := w.Result().Cookies()[0]
	if !c.HttpOnly || !c.Secure || c.Path != "/at/auth/passkeys/" || c.SameSite != http.SameSiteStrictMode || c.MaxAge != 300 || c.Domain != "" {
		t.Fatalf("ceremony cookie: %+v", c)
	}
	return result.PublicKey.Challenge, c
}

func NewSoftwarePasskey(t *testing.T) SoftwarePasskey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		t.Fatal(err)
	}
	return SoftwarePasskey{k, id}
}

func passkeyTestCBOR(v any) []byte {
	head := func(major byte, n int) []byte {
		if n < 24 {
			return []byte{major<<5 | byte(n)}
		}
		if n < 256 {
			return []byte{major<<5 | 24, byte(n)}
		}
		return []byte{major<<5 | 25, byte(n >> 8), byte(n)}
	}
	switch v := v.(type) {
	case string:
		return append(head(3, len(v)), []byte(v)...)
	case []byte:
		return append(head(2, len(v)), v...)
	case int:
		if v < 0 {
			return head(1, -1-v)
		}
		return head(0, v)
	case map[string]any:
		b := head(5, len(v))
		for k, v := range v {
			b = append(b, passkeyTestCBOR(k)...)
			b = append(b, passkeyTestCBOR(v)...)
		}
		return b
	default:
		panic("unsupported test CBOR")
	}
}

func (s SoftwarePasskey) Response(t *testing.T, enroll bool, challenge, origin, rp, handle string, flags byte, count uint32, badSignature bool) string {
	t.Helper()
	b64 := base64.RawURLEncoding.EncodeToString
	kind := "webauthn.get"
	if enroll {
		kind = "webauthn.create"
	}
	client, _ := json.Marshal(map[string]any{"type": kind, "challenge": challenge, "origin": origin, "crossOrigin": false})
	rpHash := sha256.Sum256([]byte(rp))
	ad := append([]byte{}, rpHash[:]...)
	ad = append(ad, flags)
	ad = binary.BigEndian.AppendUint32(ad, count)
	if enroll {
		ad = append(ad, make([]byte, 16)...)
		ad = binary.BigEndian.AppendUint16(ad, uint16(len(s.id)))
		ad = append(ad, s.id...)
		// COSE EC2 / ES256 / P-256, fixed-size affine coordinates.
		ad = append(ad, 0xa5, 0x01, 0x02, 0x03, 0x26, 0x20, 0x01, 0x21, 0x58, 0x20)
		ad = append(ad, s.key.X.FillBytes(make([]byte, 32))...)
		ad = append(ad, 0x22, 0x58, 0x20)
		ad = append(ad, s.key.Y.FillBytes(make([]byte, 32))...)
	}
	clientHash := sha256.Sum256(client)
	signed := append(append([]byte{}, ad...), clientHash[:]...)
	digest := sha256.Sum256(signed)
	sig, err := ecdsa.SignASN1(rand.Reader, s.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	if badSignature {
		sig[len(sig)-1] ^= 0xff
	}
	response := map[string]any{"clientDataJSON": b64(client)}
	if enroll {
		response["attestationObject"] = b64(passkeyTestCBOR(map[string]any{"fmt": "packed", "authData": ad, "attStmt": map[string]any{"alg": -7, "sig": sig}}))
		response["transports"] = []string{"internal"}
	} else {
		response["authenticatorData"], response["signature"], response["userHandle"] = b64(ad), b64(sig), b64([]byte(handle))
	}
	blob, _ := json.Marshal(map[string]any{"id": b64(s.id), "rawId": b64(s.id), "type": "public-key", "response": response})
	return string(blob)
}

type SoftwarePasskey struct {
	key *ecdsa.PrivateKey
	id  []byte
}

// Password is the plaintext every fixture account signs in with.
const Password = "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

// PasswordHash is the stored hash for fixture accounts. TestMain must set it
// from nativeauth.LowerPasswordCostForTests(Password) before m.Run: an encoded
// hash is verified with its own iteration count, so a fixture storing a
// default-cost hash would pay full production cost (about 1.5s under -race)
// on every sign-in.
var PasswordHash string
