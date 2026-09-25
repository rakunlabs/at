package server

import (
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
)

func TestArbitraryHandlerVariableViewsExcludeSecrets(t *testing.T) {
	store := newFakeVariableStore()
	store.vars["public"] = &service.Variable{ID: "public", Key: "region", Value: "eu", Secret: false}
	store.vars["secret"] = &service.Variable{ID: "secret", Key: "api_token", Value: "never-expose", Secret: true}
	ctx := executiontest.Context(t)

	lookup := nonSecretVariableLookup(ctx, store)
	if got, err := lookup("region"); err != nil || got != "eu" {
		t.Fatalf("non-secret lookup = %q, %v", got, err)
	}
	if got, err := lookup("api_token"); err == nil || got != "" || !strings.Contains(err.Error(), "approved variable reference") {
		t.Fatalf("secret lookup = %q, %v", got, err)
	}

	listed, err := nonSecretVariableLister(ctx, store)()
	if err != nil {
		t.Fatal(err)
	}
	if listed["region"] != "eu" {
		t.Fatalf("non-secret variable missing: %+v", listed)
	}
	if _, ok := listed["api_token"]; ok {
		t.Fatalf("secret variable leaked to arbitrary handler: %+v", listed)
	}

	runtimeListed, err := (&Server{variableStore: store}).runtimeVariableLister(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := runtimeListed["api_token"]; ok {
		t.Fatalf("secret variable leaked to runtime bash handler: %+v", runtimeListed)
	}
}
