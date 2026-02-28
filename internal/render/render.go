package render

import (
	"bytes"
	"log/slog"

	"github.com/rytsh/mugo/fstore"
	_ "github.com/rytsh/mugo/fstore/registry"
	"github.com/rytsh/mugo/render"
	"github.com/rytsh/mugo/templatex"
)

var ExecuteWithData = render.ExecuteWithData

// ExecuteWithFuncs renders a Go template with the standard mugo function map
// plus additional custom functions. Use this to inject per-execution functions
// like getVar that need access to runtime state.
func ExecuteWithFuncs(content string, data any, extraFuncs map[string]any) ([]byte, error) {
	tpl := templatex.New(
		templatex.WithAddFuncMapWithOpts(func(o templatex.Option) map[string]any {
			return fstore.FuncMap(
				fstore.WithLog(slog.Default()),
				fstore.WithTrust(true),
				fstore.WithExecuteTemplate(o.T),
			)
		}),
		templatex.WithAddFuncMap(extraFuncs),
	)

	var buf bytes.Buffer
	if err := tpl.Execute(
		templatex.WithIO(&buf),
		templatex.WithContent(content),
		templatex.WithData(data),
	); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
