package openai

import (
	"encoding/base64"
	"io"
	"mime/multipart"
	"strings"
)

// encodeBase64 encodes raw bytes to a standard base64 string.
func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// decodeBase64 decodes a base64 string to raw bytes.
// It handles both standard and URL-safe base64 encoding,
// as well as data URIs (e.g. "data:audio/mpeg;base64,...").
func decodeBase64(s string) ([]byte, error) {
	// Strip data URI prefix if present.
	if idx := strings.Index(s, ";base64,"); idx >= 0 {
		s = s[idx+8:]
	}

	// Try standard encoding first, then URL-safe.
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(s)
	}
	return data, err
}

// newMultipartWriter creates a multipart writer for building form-data requests.
func newMultipartWriter(w io.Writer) *multipart.Writer {
	return multipart.NewWriter(w)
}

// extensionFromContentType returns a file extension for a given MIME type.
func extensionFromContentType(ct string) string {
	switch {
	case strings.Contains(ct, "mpeg"), strings.Contains(ct, "mp3"):
		return ".mp3"
	case strings.Contains(ct, "wav"):
		return ".wav"
	case strings.Contains(ct, "ogg"):
		return ".ogg"
	case strings.Contains(ct, "flac"):
		return ".flac"
	case strings.Contains(ct, "webm"):
		return ".webm"
	case strings.Contains(ct, "mp4"), strings.Contains(ct, "m4a"):
		return ".m4a"
	default:
		return ".bin"
	}
}
