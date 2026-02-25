package workflow

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sync"

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
func SetupGojaVM(vm *goja.Runtime, inputs map[string]any) error {
	// Register global helper functions.
	if err := registerGojaHelpers(vm); err != nil {
		return err
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

	return nil
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
