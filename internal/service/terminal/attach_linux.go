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
	// -d makes control transfer explicit: one attached writer per terminal.
	cmd := exec.Command("tmux", "-S", socket, "attach-session", "-d", "-t", "shell")
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

func (a *Attachment) Close() {
	a.once.Do(func() {
		a.File.Close()
		_ = a.cmd.Process.Kill() // attached client only; the systemd-owned server survives
		_ = a.cmd.Wait()
	})
}
