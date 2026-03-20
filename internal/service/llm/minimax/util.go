package minimax

import "encoding/base64"

// encodeBase64 encodes raw bytes to a standard base64 string.
func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
