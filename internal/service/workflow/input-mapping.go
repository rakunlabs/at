package workflow

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// input_mappings maps a target input port to an RFC 6901 JSON Pointer into the
// original gathered inputs. All mappings resolve against the same input, never
// against another mapping's result. Existing graphs without mappings are unchanged.
func mapNodeInputs(noder Noder, config map[string]any, inputs map[string]any) (map[string]any, error) {
	raw, exists := config["input_mappings"]
	if !exists || raw == nil {
		return inputs, nil
	}
	mappings, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("input_mappings must be an object of input ports and JSON Pointer paths")
	}
	if len(mappings) > 64 {
		return nil, fmt.Errorf("input_mappings exceeds 64 entries")
	}
	allowed := make(map[string]bool)
	if meta, ok := noder.(NodeMetaProvider); ok {
		for _, port := range meta.Meta().Inputs {
			allowed[port.Name] = true
		}
	} else {
		for port := range inputs {
			allowed[port] = true
		}
	}
	result := make(map[string]any, len(inputs)+len(mappings))
	for key, value := range inputs {
		result[key] = value
	}
	ports := make([]string, 0, len(mappings))
	for port := range mappings {
		ports = append(ports, port)
	}
	sort.Strings(ports)
	for _, port := range ports {
		if !allowed[port] {
			return nil, fmt.Errorf("input mapping targets unknown input port %q", port)
		}
		path, ok := mappings[port].(string)
		if !ok {
			return nil, fmt.Errorf("input mapping for %q must be a JSON Pointer string", port)
		}
		value, err := resolveInputPointer(inputs, path)
		if err != nil {
			return nil, fmt.Errorf("input mapping for %q at %q: %w", port, path, err)
		}
		result[port] = value
	}
	return result, nil
}

// ValidateJSONPointer validates syntax independently of whether a field exists.
func ValidateJSONPointer(path string) error {
	_, err := parseInputPointer(path)
	return err
}

// ResolveJSONPointer selects a JSON field without coercing its value or type.
func ResolveJSONPointer(value any, path string) (any, error) { return resolveInputPointer(value, path) }

func parseInputPointer(path string) ([]string, error) {
	if len(path) > 2048 {
		return nil, fmt.Errorf("path exceeds 2048 bytes")
	}
	if path == "" {
		return nil, nil
	}
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("path must start with / (for example /prompt/customer/message)")
	}
	segments := strings.Split(path[1:], "/")
	if len(segments) > 32 {
		return nil, fmt.Errorf("path exceeds 32 segments")
	}
	for index, segment := range segments {
		for i := 0; i < len(segment); i++ {
			if segment[i] == '~' {
				if i+1 == len(segment) || (segment[i+1] != '0' && segment[i+1] != '1') {
					return nil, fmt.Errorf("invalid ~ escape in path")
				}
				i++
			}
		}
		segments[index] = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
	}
	return segments, nil
}

func resolveInputPointer(value any, path string) (any, error) {
	keys, err := parseInputPointer(path)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		v := reflect.ValueOf(value)
		switch v.Kind() {
		case reflect.Map:
			if v.Type().Key().Kind() != reflect.String {
				return nil, fmt.Errorf("cannot select a field from a non-string-keyed map")
			}
			field := v.MapIndex(reflect.ValueOf(key).Convert(v.Type().Key()))
			if !field.IsValid() {
				return nil, fmt.Errorf("field %q is missing; inspect the input or update the mapping", key)
			}
			value = field.Interface()
		case reflect.Array, reflect.Slice:
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || index >= v.Len() || strconv.Itoa(index) != key {
				return nil, fmt.Errorf("array index %q does not exist", key)
			}
			value = v.Index(index).Interface()
		default:
			return nil, fmt.Errorf("cannot select %q from a scalar or null value", key)
		}
	}
	return value, nil
}

const nodePreviewMaxBytes = 64 * 1024

// Snapshot before Run can mutate its inputs, and bound nested data as well as
// strings. Omitted previews are explicit, never plausible-but-truncated values
// that the mapper could offer as real input. Full data still reaches the node.
func snapshotNodeData(data map[string]any) (map[string]any, bool) {
	encoded, err := json.Marshal(data)
	if err != nil || len(encoded) > nodePreviewMaxBytes {
		return nil, true
	}
	var snapshot map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.UseNumber()
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, true
	}
	return snapshot, false
}
