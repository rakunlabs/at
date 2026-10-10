package sandboxkube

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"

	"github.com/rakunlabs/at/internal/sandboxruntime"
	"github.com/rakunlabs/at/internal/service/container"
)

type streamFunc func(context.Context, string, []string, remotecommand.StreamOptions) error

func (d *Driver) streamExec(ctx context.Context, name string, argv []string, opts remotecommand.StreamOptions) error {
	request := d.client.CoreV1().RESTClient().Post().Resource("pods").Name(name).Namespace(d.opts.Namespace).SubResource("exec").VersionedParams(&corev1.PodExecOptions{
		Container: sandboxContainer, Command: argv, Stdin: opts.Stdin != nil, Stdout: opts.Stdout != nil, Stderr: opts.Stderr != nil, TTY: opts.Tty}, scheme.ParameterCodec)
	cfg := rest.CopyConfig(d.restConfig)
	cfg.Timeout = 0 // commands/terminals are bounded by ctx, not a REST timeout
	ws, err := remotecommand.NewWebSocketExecutor(cfg, "GET", request.URL().String())
	if err != nil {
		return fmt.Errorf("create Kubernetes exec stream: %w", err)
	}
	spdy, err := remotecommand.NewSPDYExecutor(cfg, "POST", request.URL())
	if err != nil {
		return fmt.Errorf("create Kubernetes exec fallback: %w", err)
	}
	executor, err := remotecommand.NewFallbackExecutor(ws, spdy, func(err error) bool { return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err) })
	if err != nil {
		return fmt.Errorf("create Kubernetes exec transport: %w", err)
	}
	return executor.StreamWithContext(ctx, opts)
}

func runID() string { return strings.ToLower(rand.Text()) }

func (d *Driver) cancelCommand(handle, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pod, err := d.pod(ctx, handle)
	if err != nil {
		slog.Warn("sandbox cancellation could not resolve pod", "error", err.Error())
		d.cancellationFailed(err)
		return
	}
	if err := d.stream(ctx, pod.Name, []string{sandboxruntime.Launcher, "cancel", id}, remotecommand.StreamOptions{Stdout: io.Discard, Stderr: io.Discard}); err != nil {
		slog.Warn("sandbox process-group cancellation failed", "error", err.Error())
		d.cancellationFailed(err)
	}
}

func (d *Driver) cancellationFailed(err error) {
	if d.ownership != nil {
		d.ownership.mu.Lock()
		defer d.ownership.mu.Unlock()
		d.ownership.refuse(fmt.Errorf("remote cancellation could not be confirmed: %w", err))
	}
}

func (d *Driver) Exec(ctx context.Context, handle string, req container.ExecRequest) (int, error) {
	if len(req.Argv) == 0 {
		return -1, fmt.Errorf("command is required")
	}
	ctx, release, err := d.operation(ctx)
	if err != nil {
		return -1, err
	}
	defer release()
	pod, err := d.pod(ctx, handle)
	if err != nil {
		return -1, fmt.Errorf("resolve exec sandbox: %w", err)
	}
	env, err := json.Marshal(req.Env)
	if err != nil {
		return -1, fmt.Errorf("encode command environment: %w", err)
	}
	id := runID()
	argv := append([]string{sandboxruntime.Launcher, "run", id, req.WorkDir, string(env)}, req.Argv...)
	err = d.stream(ctx, pod.Name, argv, remotecommand.StreamOptions{Stdin: req.Stdin, Stdout: req.Stdout, Stderr: req.Stderr})
	if ctx.Err() != nil {
		d.cancelCommand(handle, id)
		return -1, fmt.Errorf("sandbox command cancelled: %w", ctx.Err())
	}
	var exit utilexec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitStatus(), nil
	}
	if err != nil {
		// A failed transport does not prove that the process stopped.
		d.cancelCommand(handle, id)
		return -1, fmt.Errorf("stream sandbox command: %w", err)
	}
	return 0, nil
}

func (d *Driver) Platform(ctx context.Context, handle string) (string, error) {
	var stdout bytes.Buffer
	code, err := d.Exec(ctx, handle, container.ExecRequest{Argv: []string{sandboxruntime.Launcher, "platform"}, Stdout: &stdout, Stderr: io.Discard})
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("sandbox platform helper exited %d", code)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (d *Driver) InstallFile(ctx context.Context, handle, dest string, data []byte, mode fs.FileMode) error {
	if len(data) > sandboxruntime.MaxInstallBytes {
		return fmt.Errorf("file exceeds 64 MiB")
	}
	// Helpers already came from the versioned image. Never try to overwrite the
	// read-only mount with the AT host's embedded build.
	if dest == sandboxruntime.FileHelper {
		code, err := d.Exec(ctx, handle, container.ExecRequest{Argv: []string{sandboxruntime.Launcher, "platform"}, Stdout: io.Discard, Stderr: io.Discard})
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("helper verification failed")
		}
		return nil
	}
	pod, err := d.pod(ctx, handle)
	if err != nil {
		return err
	}
	root := pod.Annotations[homePathAnnotation]
	if root == "" || !container.ValidHomePath(root) || !strings.HasPrefix(dest, root+"/") || path.Clean(dest) != dest {
		return fmt.Errorf("file installation must stay inside the configured persistent home")
	}
	rel := strings.TrimPrefix(dest, root+"/")
	var stderr bytes.Buffer
	code, err := d.Exec(ctx, handle, container.ExecRequest{Argv: []string{sandboxruntime.Launcher, "install", root, rel, strconv.FormatUint(uint64(mode), 8)}, Stdin: bytes.NewReader(data), Stdout: io.Discard, Stderr: &stderr})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("install home file: %s", strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (d *Driver) Attach(ctx context.Context, handle, workDir string, cols, rows uint16) (container.Terminal, error) {
	ctx, release, err := d.operation(ctx)
	if err != nil {
		return nil, err
	}
	pod, err := d.pod(ctx, handle)
	if err != nil {
		release()
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	inputRead, inputWrite := io.Pipe()
	outputRead, outputWrite := io.Pipe()
	term := &terminal{input: inputWrite, output: outputRead, cancel: cancel, contextDone: ctx.Done(), done: make(chan struct{}), sizes: make(chan remotecommand.TerminalSize, 1)}
	_ = term.Resize(cols, rows)
	id := runID()
	go func() {
		defer release()
		defer close(term.done)
		err := d.stream(ctx, pod.Name, []string{sandboxruntime.Launcher, "run-shell", id, workDir, "{}"}, remotecommand.StreamOptions{Stdin: inputRead, Stdout: outputWrite, Tty: true, TerminalSizeQueue: term})
		_ = inputRead.CloseWithError(err)
		_ = outputWrite.CloseWithError(err)
		// Closing a stream alone is not process-group cancellation in Kubernetes.
		// A successful shell exit already ran the launcher's descendant cleanup.
		if err != nil {
			d.cancelCommand(handle, id)
		}
	}()
	return term, nil
}

type terminal struct {
	input       *io.PipeWriter
	output      *io.PipeReader
	cancel      context.CancelFunc
	done        chan struct{}
	contextDone <-chan struct{}
	sizes       chan remotecommand.TerminalSize
	once        sync.Once
}

func (t *terminal) Read(p []byte) (int, error)  { return t.output.Read(p) }
func (t *terminal) Write(p []byte) (int, error) { return t.input.Write(p) }
func (t *terminal) Resize(cols, rows uint16) error {
	select {
	case <-t.done:
		return io.ErrClosedPipe
	default:
	}
	select {
	case t.sizes <- remotecommand.TerminalSize{Width: cols, Height: rows}:
	default:
		select {
		case <-t.sizes:
		default:
		}
		select {
		case t.sizes <- remotecommand.TerminalSize{Width: cols, Height: rows}:
		default:
		}
	}
	return nil
}
func (t *terminal) Next() *remotecommand.TerminalSize {
	select {
	case <-t.contextDone:
		return nil
	case <-t.done:
		return nil
	case size := <-t.sizes:
		return &size
	}
}
func (t *terminal) Close() error {
	t.once.Do(func() { t.cancel(); _ = t.input.Close(); _ = t.output.Close() })
	// Do not release Manager activity while remote cancellation is still in
	// flight: idle cleanup/reset could delete the pod underneath that request.
	select {
	case <-t.done:
		return nil
	case <-time.After(15 * time.Second):
		return fmt.Errorf("timed out waiting for remote terminal shutdown")
	}
}
