package server

import (
	"fmt"
	"slices"
	"strings"
)

// unsupportedInputError names the modalities a model is known not to accept.
type unsupportedInputError struct {
	model    string
	missing  []string
	accepted []string
}

func (e *unsupportedInputError) Error() string {
	return fmt.Sprintf("model %q does not accept %s input (accepts: %s); choose a model that does, or declare the capability under the provider's model capabilities if this is wrong",
		e.model, strings.Join(e.missing, ", "), strings.Join(e.accepted, ", "))
}

// checkInputModalities refuses input the target is known not to read.
// Unknown capabilities admit everything.
func checkInputModalities(info ProviderInfo, fullModel, actualModel string, needed []string) error {
	if len(needed) == 0 {
		return nil
	}
	caps := info.resolvedCapabilities(actualModel)
	if caps.InputModalities == nil {
		return nil
	}
	var missing []string
	for _, m := range needed {
		if !slices.Contains(caps.InputModalities, m) {
			missing = append(missing, m)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	return &unsupportedInputError{model: fullModel, missing: missing, accepted: caps.InputModalities}
}

// admitChainInputs marks targets that cannot read the request's inputs as
// failed, so a fallback chain skips them. It returns the primary's error when
// no target is left, for a single, deterministic 400.
func admitChainInputs(chain []chatCallTarget, needed []string) ([]chatCallTarget, error) {
	if len(needed) == 0 {
		return chain, nil
	}
	var first error
	usable := 0
	for i := range chain {
		if chain[i].err != nil {
			continue
		}
		if err := checkInputModalities(chain[i].info, chain[i].fullModel, chain[i].actualModel, needed); err != nil {
			chain[i].err = err
			if first == nil {
				first = err
			}
			continue
		}
		usable++
	}
	if usable == 0 && first != nil {
		return chain, first
	}

	return chain, nil
}

func unsupportedInputBody(err error) map[string]any {
	return map[string]any{
		"error": map[string]any{
			"message": err.Error(),
			"type":    "invalid_request_error",
			"param":   "messages",
			"code":    "unsupported_input_modality",
		},
	}
}
