// Package terminal implements persistent host terminals, not agent execution.
package terminal

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/oklog/ulid/v2"
)

type Host struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

type User struct {
	Name  string `json:"name"`
	UID   string `json:"uid"`
	Home  string `json:"home"`
	Shell string `json:"shell"`
}

func LocalHost() Host {
	name, _ := os.Hostname()
	h := Host{Name: name}
	machine, err := os.ReadFile("/etc/machine-id")
	if err != nil || len(strings.TrimSpace(string(machine))) == 0 {
		h.Reason = "A stable /etc/machine-id is required"
		return h
	}
	h.ID = fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(string(machine)))))[:32]
	if os.Geteuid() != 0 {
		h.Reason = "Host terminals require AT to run as root"
		return h
	}
	for _, name := range []string{"tmux", "systemd-run", "systemctl", "getent"} {
		if _, err := exec.LookPath(name); err != nil {
			h.Reason = name + " is not installed"
			return h
		}
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		h.Reason = "A running systemd system manager is required"
		return h
	}
	h.Available = true
	return h
}

func Users(ctx context.Context) ([]User, error) {
	out, err := exec.CommandContext(ctx, "getent", "passwd").Output()
	if err != nil {
		return nil, fmt.Errorf("list Linux users: %w", err)
	}
	users := []User{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, ":")
		if len(f) != 7 || f[0] == "" || !strings.HasPrefix(f[6], "/") || strings.HasSuffix(f[6], "/nologin") || strings.HasSuffix(f[6], "/false") {
			continue
		}
		users = append(users, User{Name: f[0], UID: f[2], Home: f[5], Shell: f[6]})
	}
	return users, nil
}

func lookupUser(ctx context.Context, name string) (*User, error) {
	users, err := Users(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if u.Name == name {
			return &u, nil
		}
	}
	return nil, fmt.Errorf("Linux user %q has no interactive login shell", name)
}

func validID(id string) error {
	if _, err := ulid.ParseStrict(id); err != nil {
		return fmt.Errorf("invalid terminal ID: %w", err)
	}
	return nil
}

func unitName(id string) string   { return "at-terminal-" + id + ".service" }
func socketPath(id string) string { return "/run/at-terminal-" + id + "/tmux.sock" }

// Start launches a detached tmux server in its own system service/cgroup.
// RemainAfterExit keeps the unit addressable after tmux's forking parent exits.
// No relationship to the AT service is established, so AT restarts only detach.
func Start(ctx context.Context, id, username string) error {
	if err := validID(id); err != nil {
		return err
	}
	h := LocalHost()
	if !h.Available {
		return fmt.Errorf("terminal unavailable: %s", h.Reason)
	}
	unlock, err := lockSession(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	u, err := lookupUser(ctx, username)
	if err != nil {
		return err
	}
	if Alive(ctx, id) {
		return nil
	}
	// A dead/empty unit may remain after a shell exits. Stop only this exact,
	// server-generated terminal unit before restarting an explicitly chosen shell.
	_ = exec.CommandContext(ctx, "systemctl", "stop", unitName(id)).Run()
	_ = exec.CommandContext(ctx, "systemctl", "reset-failed", unitName(id)).Run()
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("locate tmux: %w", err)
	}
	args := []string{"--unit=" + unitName(id), "--collect", "--quiet",
		"--property=Type=forking", "--property=GuessMainPID=no", "--property=RemainAfterExit=yes",
		"--property=KillMode=control-group", "--property=User=" + u.Name,
		"--property=WorkingDirectory=" + u.Home, "--property=UMask=0077",
		"--property=RuntimeDirectory=at-terminal-" + id, "--property=RuntimeDirectoryMode=0700",
		"--setenv=HOME=" + u.Home, "--setenv=USER=" + u.Name, "--setenv=LOGNAME=" + u.Name,
		"--setenv=SHELL=" + u.Shell, "--setenv=TERM=xterm-256color", "--setenv=LANG=C.UTF-8",
		tmux}
	args = append(args, tmuxStartArgs(socketPath(id), u.Shell)...)
	if out, err := exec.CommandContext(ctx, "systemd-run", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("start persistent terminal: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func tmuxStartArgs(socket, shell string) []string {
	return []string{"-S", socket, "-f", "/dev/null", "new-session", "-d", "-s", "shell", "-x", "120", "-y", "30", shell, "-l",
		";", "set-option", "-g", "status", "off", ";", "set-option", "-g", "prefix", "None",
		";", "unbind-key", "-a", ";", "set-option", "-g", "mouse", "off"}
}

func Alive(ctx context.Context, id string) bool {
	if validID(id) != nil {
		return false
	}
	return exec.CommandContext(ctx, "tmux", "-S", socketPath(id), "has-session", "-t", "shell").Run() == nil
}

func Stop(ctx context.Context, id string) error {
	if err := validID(id); err != nil {
		return err
	}
	unlock, err := lockSession(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	if out, err := exec.CommandContext(ctx, "systemctl", "stop", unitName(id)).CombinedOutput(); err != nil {
		// Already-ended sessions can still be removed from the UI.
		if !strings.Contains(string(out), "not loaded") && !strings.Contains(string(out), "not found") {
			return fmt.Errorf("stop terminal: %s: %w", strings.TrimSpace(string(out)), err)
		}
	}
	return nil
}
