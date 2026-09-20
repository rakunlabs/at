package server

import (
	"encoding/json"
	"net/http"
	"reflect"
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
	// Collection endpoints also return bare slices or {items: ...} envelopes.
	// Normalize only those collection positions, never optional nested fields,
	// byte payloads, custom JSON types or missing individual records.
	if code == http.StatusOK {
		msg = responseCollection(msg)
		if envelope, ok := msg.(map[string]any); ok {
			copy := make(map[string]any, len(envelope))
			for key, value := range envelope {
				if key == "items" || key == "data" {
					value = responseCollection(value)
				}
				copy[key] = value
			}
			msg = copy
		}
	}
	v, _ := json.Marshal(msg)

	httpResponseJSONByte(w, v, code)
}

func responseCollection(value any) any {
	if _, custom := value.(json.Marshaler); custom {
		return value
	}
	v := reflect.ValueOf(value)
	if v.IsValid() && v.Kind() == reflect.Slice && v.IsNil() && v.Type().Elem().Kind() != reflect.Uint8 {
		return reflect.MakeSlice(v.Type(), 0, 0).Interface()
	}
	return value
}

func httpResponseJSONByte(w http.ResponseWriter, msg []byte, code int) {
	w.Header().Set("Content-Type", "application/json")

	w.WriteHeader(code)
	w.Write(msg)
}
