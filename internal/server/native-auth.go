package server

import (
	"net/http"

	"github.com/rakunlabs/at/internal/nativeauth"
)

func nativeError(w http.ResponseWriter, code int, message string) {
	nativeauth.WriteError(w, code, message)
}

func decodeNativeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	return nativeauth.DecodeBody(w, r, dst)
}
