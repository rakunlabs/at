package bedrock

import (
	"crypto/hmac"
	"crypto/sha256"
	"sort"
)

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// stringSortStrings is a thin wrapper to avoid the temptation of writing
// sort.Strings(...) inline (helps if we later want to support locale-aware
// ordering or stable sort).
func stringSortStrings(s []string) {
	sort.Strings(s)
}
