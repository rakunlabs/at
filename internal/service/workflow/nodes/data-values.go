package nodes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strings"

	"github.com/rakunlabs/at/internal/service/workflow"
)

const maxDataItems = 10000

func decodeDataConfig(data map[string]any, target any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode data-node configuration: %w", err)
	}
	if err := json.Unmarshal(b, target); err != nil {
		return fmt.Errorf("decode data-node configuration: %w", err)
	}
	return nil
}

func dataInput(inputs map[string]any) (any, error) {
	value, ok := inputs["data"]
	if !ok {
		return nil, fmt.Errorf("connect the data input or configure its input mapping")
	}
	return value, nil
}

func dataItems(value any, singleton bool) ([]any, error) {
	v := reflect.ValueOf(value)
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		if singleton {
			return []any{value}, nil
		}
		return nil, fmt.Errorf("expected an array; select its path or use the Input mapper")
	}
	if v.Len() > maxDataItems {
		return nil, fmt.Errorf("array exceeds %d items", maxDataItems)
	}
	items := make([]any, v.Len())
	for i := range items {
		items[i] = v.Index(i).Interface()
	}
	return items, nil
}

func dataObject(value any) (map[string]any, error) {
	v := reflect.ValueOf(value)
	if v.Kind() != reflect.Map || v.Type().Key().Kind() != reflect.String || v.IsNil() {
		return nil, fmt.Errorf("expected an object")
	}
	out := make(map[string]any, v.Len())
	iter := v.MapRange()
	for iter.Next() {
		out[iter.Key().String()] = iter.Value().Interface()
	}
	return out, nil
}

type dataLiteral struct {
	ValueType string `json:"value_type"`
	Value     string `json:"value"`
}

func (l dataLiteral) parse() (any, error) {
	if len(l.Value) > 32768 {
		return nil, fmt.Errorf("literal exceeds 32 KiB")
	}
	switch l.ValueType {
	case "", "string":
		return l.Value, nil
	case "null":
		return nil, nil
	case "number", "boolean", "json":
		var value any
		if err := json.Unmarshal([]byte(l.Value), &value); err != nil {
			return nil, fmt.Errorf("invalid %s value: %w", l.ValueType, err)
		}
		if l.ValueType == "number" {
			if _, ok := value.(float64); !ok {
				return nil, fmt.Errorf("value must be a JSON number")
			}
		}
		if l.ValueType == "boolean" {
			if _, ok := value.(bool); !ok {
				return nil, fmt.Errorf("value must be true or false")
			}
		}
		return value, nil
	default:
		return nil, fmt.Errorf("unknown value_type %q", l.ValueType)
	}
}

type dataCondition struct {
	Path     string `json:"path"`
	Operator string `json:"operator"`
	dataLiteral
}

func (c dataCondition) validate() error {
	if err := workflow.ValidateJSONPointer(c.Path); err != nil {
		return err
	}
	switch c.Operator {
	case "exists", "not_exists", "is_empty":
		return nil
	case "eq", "neq", "gt", "gte", "lt", "lte", "contains":
		_, err := c.parse()
		return err
	default:
		return fmt.Errorf("unknown operator %q", c.Operator)
	}
}

func numericValue(value any) (*big.Rat, bool) {
	switch reflect.ValueOf(value).Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
	case reflect.String:
		if _, ok := value.(json.Number); !ok {
			return nil, false
		}
	default:
		return nil, false
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	number, ok := new(big.Rat).SetString(string(b))
	return number, ok
}

func jsonValuesEqual(a, b any) bool {
	// Normalize only for comparison; runtime data retains its original types.
	normalize := func(value any) (any, error) {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		var decoded any
		err = decoder.Decode(&decoded)
		return decoded, err
	}
	left, err := normalize(a)
	if err != nil {
		return false
	}
	right, err := normalize(b)
	if err != nil {
		return false
	}
	return equalJSON(left, right)
}

func equalJSON(a, b any) bool {
	if left, ok := a.(json.Number); ok {
		right, ok := b.(json.Number)
		if !ok {
			return false
		}
		x, xok := numericValue(left)
		y, yok := numericValue(right)
		return xok && yok && x.Cmp(y) == 0
	}
	switch left := a.(type) {
	case map[string]any:
		right, ok := b.(map[string]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for key, value := range left {
			other, exists := right[key]
			if !exists || !equalJSON(value, other) {
				return false
			}
		}
		return true
	case []any:
		right, ok := b.([]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for i := range left {
			if !equalJSON(left[i], right[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}

func (c dataCondition) matches(item any) (bool, error) {
	actual, err := workflow.ResolveJSONPointer(item, c.Path)
	if c.Operator == "not_exists" {
		return err != nil, nil
	}
	if err != nil {
		return false, nil
	}
	if c.Operator == "exists" {
		return true, nil
	} // null is a value, not a missing path
	if c.Operator == "is_empty" {
		if actual == nil {
			return true, nil
		}
		v := reflect.ValueOf(actual)
		switch v.Kind() {
		case reflect.String, reflect.Slice, reflect.Array, reflect.Map:
			return v.Len() == 0, nil
		}
		return false, nil
	}
	expected, err := c.parse()
	if err != nil {
		return false, err
	}
	switch c.Operator {
	case "eq":
		return jsonValuesEqual(actual, expected), nil
	case "neq":
		return !jsonValuesEqual(actual, expected), nil
	case "contains":
		if text, ok := actual.(string); ok {
			part, ok := expected.(string)
			return ok && strings.Contains(text, part), nil
		}
		kind := reflect.ValueOf(actual).Kind()
		if kind != reflect.Array && kind != reflect.Slice {
			return false, nil
		}
		items, err := dataItems(actual, false)
		if err != nil {
			return false, fmt.Errorf("contains: %w", err)
		}
		for _, value := range items {
			if jsonValuesEqual(value, expected) {
				return true, nil
			}
		}
		return false, nil
	default:
		left, lok := numericValue(actual)
		right, rok := numericValue(expected)
		if !lok || !rok {
			return false, nil
		}
		cmp := left.Cmp(right)
		switch c.Operator {
		case "gt":
			return cmp > 0, nil
		case "gte":
			return cmp >= 0, nil
		case "lt":
			return cmp < 0, nil
		case "lte":
			return cmp <= 0, nil
		}
	}
	return false, nil
}

func finiteNumber(value any) (float64, error) {
	number, ok := numericValue(value)
	if !ok {
		return 0, fmt.Errorf("expected a number (numeric strings are not converted)")
	}
	f, _ := number.Float64()
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, fmt.Errorf("number is outside the supported range")
	}
	return f, nil
}

func standardDataPorts() ([]workflow.PortMeta, []workflow.PortMeta) {
	return []workflow.PortMeta{{Name: "data", Type: workflow.PortTypeData, Accept: []workflow.PortType{workflow.PortTypeText}, Label: "Data", Position: "left"}},
		[]workflow.PortMeta{{Name: "data", Type: workflow.PortTypeData, Label: "Data", Position: "right"}}
}
