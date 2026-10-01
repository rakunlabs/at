package server

import (
	"net/http"

	"github.com/rakunlabs/at/internal/httpx"
)

type responseMessage = httpx.Message

func httpResponse(w http.ResponseWriter, msg string, code int) {
	httpx.Response(w, msg, code)
}

func httpResponseJSON(w http.ResponseWriter, msg any, code int) {
	httpx.JSON(w, msg, code)
}

func httpResponseJSONByte(w http.ResponseWriter, msg []byte, code int) {
	httpx.JSONBytes(w, msg, code)
}
