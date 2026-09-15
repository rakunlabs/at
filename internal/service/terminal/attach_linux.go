package terminal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"

	"sync"
	"syscall"

	"github.com/creack/pty"
)

type Attachment struct {
	File *os.File
	cmd  *exec.Cmd
	once sync.Once
}

func Attach(ctx context.Context, id, username string, cols, rows uint16) (*Attachment, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	if !Alive(ctx, id) {
		return nil, fmt.Errorf("terminal session ended; start a new shell explicitly")
	}
	return attachSocket(ctx, socketPath(id), username, cols, rows)
}

func attachSocket(ctx context.Context, socket, username string, cols, rows uint16) (*Attachment, error) {
	u, err := lookupUser(ctx, username)
	if err != nil {
		return nil, err
	}
	account, err := user.Lookup(username)
	if err != nil {
		return nil, fmt.Errorf("resolve Linux identity: %w", err)
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("parse Linux UID: %w", err)
	}
	gid, err := strconv.ParseUint(account.Gid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("parse Linux GID: %w", err)
	}
	groups, err := account.GroupIds()
	if err != nil {
		return nil, fmt.Errorf("resolve Linux groups: %w", err)
	}
	credential := &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
	if os.Geteuid() != 0 {
		credential.NoSetGroups = true
	}
	for _, group := range groups {
		g, err := strconv.ParseUint(group, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("parse Linux group: %w", err)
		}
		credential.Groups = append(credential.Groups, uint32(g))
	}
	// Several clients may attach at once so a terminal can be watched from a
	// second device. Control is not enforced here: tmux cannot toggle a live
	// client between read-only and read-write, so -r would make every handover a
	// kill-and-reattach. The PTY master below is private to this process, and the
	// caller writes only the holder's bytes into it, so a viewer's input has no
	// path to the shell. The session also has no key bindings and no prefix
	// (see tmuxStartArgs), leaving an attached client nothing else to drive.
	//
	// window-size manual keeps the window at the size the caller sets instead of
	// letting whichever client attached last shrink it; a phone watching a
	// desktop shell would otherwise reflow the writer's screen. Best effort: tmux
	// before 3.1 has no such option and keeps its own sizing.
	_ = exec.CommandContext(ctx, "tmux", "-S", socket, "set-option", "-g", "window-size", "manual").Run()
	cmd := exec.Command("tmux", "-S", socket, "attach-session", "-t", "shell")
	cmd.Dir = u.Home
	// Never inherit AT's database credentials, provider keys, or bootstrap env.
	cmd.Env = []string{"HOME=" + u.Home, "USER=" + u.Name, "LOGNAME=" + u.Name, "SHELL=" + u.Shell,
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TERM=xterm-256color", "LANG=C.UTF-8"}
	f, err := pty.StartWithAttrs(cmd, &pty.Winsize{Cols: cols, Rows: rows}, &syscall.SysProcAttr{Setsid: true, Setctty: true, Credential: credential})
	if err != nil {
		return nil, fmt.Errorf("attach terminal PTY: %w", err)
	}
	return &Attachment{File: f, cmd: cmd}, nil
}

func (a *Attachment) Resize(cols, rows uint16) error {
	if cols < 2 || cols > 500 || rows < 2 || rows > 300 {
		return fmt.Errorf("terminal size out of range")
	}
	return pty.Setsize(a.File, &pty.Winsize{Cols: cols, Rows: rows})
}

// ResizeWindow sets the shared window, which is what the programs in the shell
// see. Only the client holding control should call it; a viewer resizes just its
// own viewport through Resize and is letterboxed when it is smaller. Best effort
// for the same reason as the window-size option above.
func ResizeWindow(ctx context.Context, id string, cols, rows uint16) {
	if validID(id) != nil || cols < 2 || cols > 500 || rows < 2 || rows > 300 {
		return
	}
	_ = exec.CommandContext(ctx, "tmux", "-S", socketPath(id), "resize-window", "-t", "shell",
		"-x", strconv.FormatUint(uint64(cols), 10), "-y", strconv.FormatUint(uint64(rows), 10)).Run()
}

func (a *Attachment) Close() {
	a.once.Do(func() {
		a.File.Close()
		_ = a.cmd.Process.Kill() // attached client only; the systemd-owned server survives
		_ = a.cmd.Wait()
	})
}
