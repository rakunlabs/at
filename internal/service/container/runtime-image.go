package container

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os/exec"
	"strings"
)

// runtimeImageRecipe derives a runnable image from an arbitrary base: it adds
// bash, git, python3 and GNU coreutils when the base lacks them and keeps the
// container alive with sleep, because stock images (debian, ubuntu, python…)
// exit as soon as `docker run -d` starts them. Installing at build time is also
// the only option: containers run with every capability dropped, so apt cannot
// work inside them.
const runtimeImageRecipe = `FROM %s
USER root
RUN set -e; missing=; for c in bash git python3 timeout sleep; do command -v "$c" >/dev/null 2>&1 || missing=1; done; \
  if [ -n "$missing" ]; then \
    if command -v apt-get >/dev/null 2>&1; then export DEBIAN_FRONTEND=noninteractive; apt-get update; apt-get install -y --no-install-recommends bash git python3 coreutils ca-certificates; rm -rf /var/lib/apt/lists/*; \
    elif command -v apk >/dev/null 2>&1; then apk add --no-cache bash git python3 coreutils ca-certificates; \
    elif command -v dnf >/dev/null 2>&1; then dnf install -y bash git python3 coreutils ca-certificates; dnf clean all; \
    elif command -v microdnf >/dev/null 2>&1; then microdnf install -y bash git python3 coreutils ca-certificates; microdnf clean all; \
    else echo "base image has no supported package manager (apt, apk, dnf); it must already provide bash, git, python3 and GNU coreutils" >&2; exit 1; fi; \
  fi
WORKDIR /workspace
ENTRYPOINT ["sleep", "infinity"]
CMD []
`

// RuntimeImageTag is the local tag of the image derived from base. It changes
// with the recipe, so a recipe update rebuilds instead of reusing a stale image.
func RuntimeImageTag(base string) string {
	sum := sha256.Sum256([]byte(runtimeImageRecipe + "\x00" + base))
	return fmt.Sprintf("at-developer-runtime:%x", sum[:8])
}

// resolveImage returns the image a container for cfg is started from, building
// the derived runtime image on first use. It never holds m.mu, so a build (which
// can take a while) does not block other scopes' commands.
func (m *Manager) resolveImage(ctx context.Context, cfg Config) (string, error) {
	base := baseImage(cfg)
	if !cfg.ProvisionTools {
		return base, nil
	}
	if strings.ContainsAny(base, " \t\r\n\\") || strings.HasPrefix(base, "-") {
		return "", fmt.Errorf("invalid image reference %q", base)
	}

	m.imageMu.Lock()
	defer m.imageMu.Unlock()
	if tag, ok := m.images[base]; ok {
		return tag, nil
	}
	tag := RuntimeImageTag(base)
	if err := exec.CommandContext(ctx, "docker", "image", "inspect", tag).Run(); err != nil {
		if err := buildRuntimeImage(ctx, base, tag); err != nil {
			return "", err
		}
	}
	m.images[base] = tag
	return tag, nil
}

func baseImage(cfg Config) string {
	if cfg.Image == "" {
		return DefaultConfig().Image
	}
	return cfg.Image
}

func (m *Manager) forgetImage(base string) {
	m.imageMu.Lock()
	defer m.imageMu.Unlock()
	delete(m.images, base)
}

func buildRuntimeImage(ctx context.Context, base, tag string) error {
	cmd := exec.CommandContext(ctx, "docker", "build", "--label", "at.managed=true", "-t", tag, "-")
	cmd.Stdin = strings.NewReader(fmt.Sprintf(runtimeImageRecipe, base))
	output := newBoundedOutput(64 << 10)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("prepare runtime image from %s: %s: %w", base, lastLines(output.String(), 8), err)
	}
	return nil
}

func lastLines(text string, n int) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
