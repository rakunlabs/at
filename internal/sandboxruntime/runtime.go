// Package sandboxruntime supplies the static launcher installed by the
// Kubernetes helper init container. It needs no shell, tar or Python.
package sandboxruntime

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const Directory = "/opt/at-runtime"
const Launcher = Directory + "/at-sandbox"
const FileHelper = Directory + "/at-devfs"
const MaxInstallBytes = 64 << 20

// Install writes atomically under root. os.Root prevents path traversal and
// symlink escapes even when another process changes the tree during the write.
func Install(root, relative string, mode fs.FileMode, src io.Reader) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || !filepath.IsLocal(relative) || mode & ^fs.FileMode(0o777) != 0 {
		return fmt.Errorf("invalid installation path or mode")
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open installation root: %w", err)
	}
	defer r.Close()
	parent := filepath.Dir(relative)
	if err := r.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create installation directory: %w", err)
	}
	tmp := filepath.Join(parent, ".at-install-"+rand.Text())
	f, err := r.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create installation file: %w", err)
	}
	defer r.Remove(tmp)
	count, copyErr := io.Copy(f, io.LimitReader(src, MaxInstallBytes+1))
	if copyErr == nil && count > MaxInstallBytes {
		copyErr = fmt.Errorf("file exceeds the 64 MiB installation limit")
	}
	if copyErr == nil {
		copyErr = f.Chmod(mode)
	}
	if copyErr == nil {
		copyErr = f.Sync()
	}
	closeErr := f.Close()
	if copyErr != nil {
		return fmt.Errorf("write installation file: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close installation file: %w", closeErr)
	}
	if err := r.Rename(tmp, relative); err != nil {
		return fmt.Errorf("replace installation file: %w", err)
	}
	return nil
}

// Command resolves exact argv and environment without a shell interpolation.
func Command(dir, environment string, argv []string) (string, []string, []string, error) {
	if len(argv) == 0 || argv[0] == "" {
		return "", nil, nil, fmt.Errorf("command is required")
	}
	if dir != "" {
		if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
			return "", nil, nil, fmt.Errorf("invalid working directory")
		}
		if err := os.Chdir(dir); err != nil {
			return "", nil, nil, fmt.Errorf("change working directory: %w", err)
		}
	}
	var values map[string]string
	if len(environment) > 64<<10 {
		return "", nil, nil, fmt.Errorf("environment exceeds 64 KiB")
	}
	if err := json.Unmarshal([]byte(environment), &values); err != nil {
		return "", nil, nil, fmt.Errorf("decode environment: %w", err)
	}
	for key, value := range values {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
			return "", nil, nil, fmt.Errorf("invalid environment variable")
		}
		if err := os.Setenv(key, value); err != nil {
			return "", nil, nil, fmt.Errorf("set environment: %w", err)
		}
	}
	for _, arg := range argv {
		if strings.ContainsRune(arg, 0) {
			return "", nil, nil, fmt.Errorf("invalid command argument")
		}
	}
	file, err := exec.LookPath(argv[0])
	if err != nil {
		return "", nil, nil, fmt.Errorf("find command: %w", err)
	}
	return file, argv, os.Environ(), nil
}

func Run(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("operation is required")
	}
	switch args[0] {
	case "platform":
		_, err := fmt.Fprintf(stdout, "%s/%s\n", runtime.GOOS, runtime.GOARCH)
		return err
	case "init":
		if len(args) != 2 {
			return fmt.Errorf("init requires a destination directory")
		}
		for _, name := range []string{"at-sandbox", "at-devfs"} {
			f, err := os.Open("/" + name)
			if err != nil {
				return fmt.Errorf("open helper: %w", err)
			}
			err = Install(args[1], name, 0o755, f)
			_ = f.Close()
			if err != nil {
				return err
			}
		}
		return nil
	case "install":
		if len(args) != 4 {
			return fmt.Errorf("install requires root, relative path and mode")
		}
		mode, err := strconv.ParseUint(args[3], 8, 32)
		if err != nil {
			return fmt.Errorf("invalid file mode: %w", err)
		}
		return Install(args[1], args[2], fs.FileMode(mode), stdin)
	case "exec", "shell":
		if len(args) < 3 {
			return fmt.Errorf("exec requires working directory, environment and argv")
		}
		argv := args[3:]
		if args[0] == "shell" {
			if _, err := exec.LookPath("bash"); err == nil {
				argv = []string{"bash", "-l"}
			} else {
				argv = []string{"sh", "-l"}
			}
		}
		file, argv, env, err := Command(args[1], args[2], argv)
		if err != nil {
			return err
		}
		return replaceProcess(file, argv, env)
	case "run", "run-shell":
		if len(args) < 4 {
			return fmt.Errorf("run requires ID, working directory, environment and argv")
		}
		argv := args[4:]
		if args[0] == "run-shell" {
			if _, err := exec.LookPath("bash"); err == nil {
				argv = []string{"bash", "-l"}
			} else {
				argv = []string{"sh", "-l"}
			}
		}
		file, argv, env, err := Command(args[2], args[3], argv)
		if err != nil {
			return err
		}
		return supervise(args[1], file, argv, env)
	case "cancel":
		if len(args) != 2 {
			return fmt.Errorf("cancel requires a run ID")
		}
		return cancelRun(args[1])
	case "idle":
		return idle()
	default:
		return fmt.Errorf("unknown operation %q", args[0])
	}
}
