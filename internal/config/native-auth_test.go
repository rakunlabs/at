package config

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rakunlabs/chu"
)

func TestNativeAuthRedactsBootstrapToken(t *testing.T) {
	cfg := Config{Server: Server{NativeAuth: &NativeAuth{Enabled: true, Origin: "https://at.example", BootstrapToken: "never-log-this-operator-secret"}}}
	logged := fmt.Sprint(chu.MarshalMap(cfg))
	if strings.Contains(logged, cfg.Server.NativeAuth.BootstrapToken) {
		t.Fatal("bootstrap token appears in startup configuration log")
	}
}
