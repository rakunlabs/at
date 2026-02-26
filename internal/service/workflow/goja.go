package workflow

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// ─── Body Wrapper ───

// BodyWrapper wraps an io.ReadCloser for lazy consumption in goja.
// The body is read on the first method call and the result is cached.
// All subsequent method calls use the cached bytes.
//
// Exposed to JavaScript as an object with methods:
//
//	body.toString()  → string
//	body.jsonParse() → parsed JSON (object/array/primitive)
//	body.toBase64()  → base64 encoded string
//	body.bytes()     → raw []byte (Uint8Array in JS)
//	body.length      → byte count
type BodyWrapper struct {
	reader io.ReadCloser
	data   []byte
	once   sync.Once
	err    error
}

// NewBodyWrapper creates a BodyWrapper from an io.ReadCloser.
func NewBodyWrapper(r io.ReadCloser) *BodyWrapper {
	return &BodyWrapper{reader: r}
}

// consume reads the full body and caches the result. Safe for concurrent use.
func (b *BodyWrapper) consume() ([]byte, error) {
	b.once.Do(func() {
		if b.reader == nil {
			b.data = []byte{}
			return
		}
		b.data, b.err = io.ReadAll(b.reader)
	})

	return b.data, b.err
}

// ToString reads the body and returns it as a string.
func (b *BodyWrapper) ToString() (string, error) {
	data, err := b.consume()
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// JsonParse reads the body and parses it as JSON.
func (b *BodyWrapper) JsonParse() (any, error) {
	data, err := b.consume()
	if err != nil {
		return nil, err
	}

	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("jsonParse: %w", err)
	}

	return parsed, nil
}

// ToBase64 reads the body and returns the base64-encoded string.
func (b *BodyWrapper) ToBase64() (string, error) {
	data, err := b.consume()
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(data), nil
}

// Bytes reads the body and returns the raw bytes.
func (b *BodyWrapper) Bytes() ([]byte, error) {
	return b.consume()
}

// Length reads the body and returns the byte count.
func (b *BodyWrapper) Length() (int, error) {
	data, err := b.consume()
	if err != nil {
		return 0, err
	}

	return len(data), nil
}

// ─── Goja VM Setup ───

// SetupGojaVM configures a goja runtime with global helper functions and
// sets all input values on the VM. Any io.ReadCloser values found in the
// input tree (including nested maps) are automatically wrapped in BodyWrapper.
// If a VarLookup is provided, a getVar(key) function is also registered.
func SetupGojaVM(vm *goja.Runtime, inputs map[string]any, varLookup ...VarLookup) error {
	// Register global helper functions.
	if err := registerGojaHelpers(vm); err != nil {
		return err
	}

	// Register getVar if a lookup function was provided.
	if len(varLookup) > 0 && varLookup[0] != nil {
		lookup := varLookup[0]
		if err := vm.Set("getVar", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				panic(vm.NewTypeError("getVar: key is required"))
			}
			key := call.Arguments[0].String()
			val, err := lookup(key)
			if err != nil {
				panic(vm.NewTypeError(fmt.Sprintf("getVar: %v", err)))
			}
			return vm.ToValue(val)
		}); err != nil {
			return err
		}
	}

	// Walk inputs and wrap io.ReadCloser values.
	wrapped := wrapReaders(inputs)

	// Set all inputs on the VM.
	for k, v := range wrapped {
		if err := vm.Set(k, v); err != nil {
			return fmt.Errorf("failed to set %q: %w", k, err)
		}
	}

	return nil
}

// registerGojaHelpers adds global utility functions to the goja VM.
//
// Available in JS:
//
//	toString(v)   — convert []byte to string
//	jsonParse(v)  — parse string or []byte as JSON
//	btoa(v)       — base64 encode []byte or string
//	atob(s)       — base64 decode string to []byte
func registerGojaHelpers(vm *goja.Runtime) error {
	// toString: convert []byte or any value to string.
	if err := vm.Set("toString", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("")
		}

		v := call.Arguments[0].Export()

		switch val := v.(type) {
		case []byte:
			return vm.ToValue(string(val))
		case string:
			return vm.ToValue(val)
		default:
			return vm.ToValue(fmt.Sprintf("%v", v))
		}
	}); err != nil {
		return err
	}

	// jsonParse: parse string or []byte as JSON.
	if err := vm.Set("jsonParse", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Null()
		}

		v := call.Arguments[0].Export()

		var raw []byte
		switch val := v.(type) {
		case []byte:
			raw = val
		case string:
			raw = []byte(val)
		default:
			panic(vm.NewTypeError("jsonParse: expected string or bytes"))
		}

		var parsed any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			panic(vm.NewTypeError("jsonParse: " + err.Error()))
		}

		return vm.ToValue(parsed)
	}); err != nil {
		return err
	}

	// btoa: base64 encode []byte or string.
	if err := vm.Set("btoa", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("")
		}

		v := call.Arguments[0].Export()

		var raw []byte
		switch val := v.(type) {
		case []byte:
			raw = val
		case string:
			raw = []byte(val)
		default:
			panic(vm.NewTypeError("btoa: expected string or bytes"))
		}

		return vm.ToValue(base64.StdEncoding.EncodeToString(raw))
	}); err != nil {
		return err
	}

	// atob: base64 decode string to []byte.
	if err := vm.Set("atob", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue([]byte{})
		}

		s := call.Arguments[0].String()

		decoded, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			panic(vm.NewTypeError("atob: " + err.Error()))
		}

		return vm.ToValue(decoded)
	}); err != nil {
		return err
	}

	// JSON_stringify: marshal a value to JSON string.
	// This is also registered in executeJSHandler but we add it here for
	// consistency so SetupGojaVM-based VMs also have it.
	if err := vm.Set("JSON_stringify", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("")
		}
		data, err := json.Marshal(call.Arguments[0].Export())
		if err != nil {
			return vm.ToValue("")
		}
		return vm.ToValue(string(data))
	}); err != nil {
		return err
	}

	// Register HTTP helper functions for making external API calls from JS.
	if err := registerGojaHTTPHelpers(vm); err != nil {
		return err
	}

	return nil
}

// httpTimeout is the default timeout for HTTP requests made from Goja JS.
const httpTimeout = 30 * time.Second

// registerGojaHTTPHelpers registers httpGet, httpPost, httpPut, httpDelete
// functions on the Goja VM. Each returns an object with:
//
//	{ status: number, headers: object, body: BodyWrapper }
//
// Available in JS:
//
//	httpGet(url, headers?)           → response object
//	httpPost(url, body?, headers?)   → response object
//	httpPut(url, body?, headers?)    → response object
//	httpDelete(url, headers?)        → response object
func registerGojaHTTPHelpers(vm *goja.Runtime) error {
	// httpGet(url, headers?)
	if err := vm.Set("httpGet", func(call goja.FunctionCall) goja.Value {
		return doHTTPRequest(vm, "GET", call.Arguments)
	}); err != nil {
		return err
	}

	// httpPost(url, body?, headers?)
	if err := vm.Set("httpPost", func(call goja.FunctionCall) goja.Value {
		return doHTTPRequest(vm, "POST", call.Arguments)
	}); err != nil {
		return err
	}

	// httpPut(url, body?, headers?)
	if err := vm.Set("httpPut", func(call goja.FunctionCall) goja.Value {
		return doHTTPRequest(vm, "PUT", call.Arguments)
	}); err != nil {
		return err
	}

	// httpDelete(url, headers?)
	if err := vm.Set("httpDelete", func(call goja.FunctionCall) goja.Value {
		return doHTTPRequest(vm, "DELETE", call.Arguments)
	}); err != nil {
		return err
	}

	return nil
}

// doHTTPRequest performs an HTTP request and returns a goja object with
// status, headers, and body fields.
//
// Argument patterns:
//
//	GET/DELETE: (url string, headers? object)
//	POST/PUT:  (url string, body? any, headers? object)
func doHTTPRequest(vm *goja.Runtime, method string, args []goja.Value) goja.Value {
	if len(args) == 0 {
		panic(vm.NewTypeError(fmt.Sprintf("http%s: url is required",
			method[0:1]+lower(method[1:]))))
	}

	url := args[0].String()

	var bodyReader io.Reader
	var headers map[string]string

	switch method {
	case "GET", "DELETE":
		// Second arg is optional headers.
		if len(args) > 1 && !goja.IsUndefined(args[1]) && !goja.IsNull(args[1]) {
			headers = exportHeaders(args[1])
		}
	case "POST", "PUT":
		// Second arg is optional body, third arg is optional headers.
		if len(args) > 1 && !goja.IsUndefined(args[1]) && !goja.IsNull(args[1]) {
			exported := args[1].Export()
			switch v := exported.(type) {
			case string:
				bodyReader = bytes.NewBufferString(v)
			default:
				// Marshal non-string bodies as JSON.
				data, err := json.Marshal(v)
				if err != nil {
					panic(vm.NewTypeError(fmt.Sprintf("http%s: marshal body: %v",
						method[0:1]+lower(method[1:]), err)))
				}
				bodyReader = bytes.NewBuffer(data)
			}
		}
		if len(args) > 2 && !goja.IsUndefined(args[2]) && !goja.IsNull(args[2]) {
			headers = exportHeaders(args[2])
		}
	}

	client := &http.Client{Timeout: httpTimeout}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		panic(vm.NewTypeError(fmt.Sprintf("http%s: create request: %v",
			method[0:1]+lower(method[1:]), err)))
	}

	// Set Content-Type for requests with a body if not explicitly set.
	if bodyReader != nil {
		if _, ok := headers["Content-Type"]; !ok {
			req.Header.Set("Content-Type", "application/json")
		}
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		panic(vm.NewTypeError(fmt.Sprintf("http%s: request failed: %v",
			method[0:1]+lower(method[1:]), err)))
	}

	// Read the full response body.
	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		panic(vm.NewTypeError(fmt.Sprintf("http%s: read response: %v",
			method[0:1]+lower(method[1:]), err)))
	}

	// Build response headers map.
	respHeaders := make(map[string]string, len(resp.Header))
	for k := range resp.Header {
		respHeaders[k] = resp.Header.Get(k)
	}

	// Try to parse body as JSON; fall back to string.
	var parsedBody any
	if err := json.Unmarshal(respBody, &parsedBody); err != nil {
		parsedBody = string(respBody)
	}

	return vm.ToValue(map[string]any{
		"status":  resp.StatusCode,
		"headers": respHeaders,
		"body":    parsedBody,
	})
}

// exportHeaders converts a goja value to a map[string]string of headers.
func exportHeaders(v goja.Value) map[string]string {
	exported := v.Export()
	m, ok := exported.(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, val := range m {
		result[k] = fmt.Sprintf("%v", val)
	}
	return result
}

// lower returns a lowercase version of a single-char string.
func lower(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// wrapReaders recursively walks a map and wraps any io.ReadCloser values
// in BodyWrapper so they are usable from goja. Returns a new map; the
// original is not modified.
func wrapReaders(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = wrapValue(v)
	}

	return out
}

// wrapValue wraps a single value: io.ReadCloser becomes BodyWrapper,
// nested maps are walked recursively, everything else passes through.
func wrapValue(v any) any {
	switch val := v.(type) {
	case io.ReadCloser:
		return NewBodyWrapper(val)
	case io.Reader:
		return NewBodyWrapper(io.NopCloser(val))
	case map[string]any:
		return wrapReaders(val)
	default:
		return v
	}
}
