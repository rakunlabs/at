package server

import (
	"encoding/json"
	"net/http"
)

type responseMessage struct {
	Message string `json:"message"`
}

func httpResponse(w http.ResponseWriter, msg string, code int) {
	v, _ := json.Marshal(responseMessage{
		Message: msg,
	})

	httpResponseJSONByte(w, v, code)
}

func httpResponseJSON(w http.ResponseWriter, msg any, code int) {
	v, _ := json.Marshal(msg)

	httpResponseJSONByte(w, v, code)
}

func httpResponseJSONByte(w http.ResponseWriter, msg []byte, code int) {
	w.Header().Set("Content-Type", "application/json")

	w.WriteHeader(code)
	w.Write(msg)
}
