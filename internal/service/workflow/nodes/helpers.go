package nodes

import (
	"github.com/rakunlabs/at/internal/service/workflow"
)

// varFuncMap builds a Go template FuncMap with a getVar function
// that resolves variables from the workflow registry.
func varFuncMap(reg *workflow.Registry) map[string]any {
	funcs := make(map[string]any)
	if reg != nil && reg.VarLookup != nil {
		funcs["getVar"] = func(key string) (string, error) {
			return reg.VarLookup(key)
		}
	}

	return funcs
}
