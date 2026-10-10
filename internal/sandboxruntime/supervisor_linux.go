package sandboxruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

type processIdentity struct {
	PID             int    `json:"pid"`
	Start           string `json:"start"`
	Supervisor      int    `json:"supervisor"`
	SupervisorStart string `json:"supervisor_start"`
}

func runDirectory(id string) (string, error) {
	if len(id) < 16 || len(id) > 64 {
		return "", fmt.Errorf("invalid run ID")
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return "", fmt.Errorf("invalid run ID")
		}
	}
	return "/tmp/.at-exec-" + id, nil
}

func processStart(pid int) (string, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", err
	}
	end := strings.LastIndex(string(data), ") ")
	if end < 0 {
		return "", fmt.Errorf("invalid process identity")
	}
	fields := strings.Fields(string(data)[end+2:])
	if len(fields) < 20 {
		return "", fmt.Errorf("invalid process identity")
	}
	return fields[19], nil
}

func supervise(id, file string, argv, env []string) error {
	dir, err := runDirectory(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer r.Close()
	if _, err := r.Stat("cancelled"); err == nil {
		return fmt.Errorf("run cancelled before execution")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	cmd := exec.Command(file, argv[1:]...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, os.Stdin, os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Adopt background jobs even when an interactive shell gave them a
	// different process group. They must not survive this command/attachment.
	if _, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, 36 /* PR_SET_CHILD_SUBREAPER */, 1, 0, 0, 0, 0); errno != 0 {
		return fmt.Errorf("configure child reaping: %w", errno)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Kubernetes allocates the TTY to the launcher. Hand its foreground group
	// to the child so Ctrl-C, job control and interactive editors work normally.
	var foreground int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), syscall.TIOCGPGRP, uintptr(unsafe.Pointer(&foreground))); errno == 0 {
		signal.Ignore(syscall.SIGTTOU)
		child := int32(cmd.Process.Pid)
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), syscall.TIOCSPGRP, uintptr(unsafe.Pointer(&child)))
		if errno != 0 {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Wait()
			return fmt.Errorf("hand terminal control to command: %w", errno)
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGCONT)
		defer func() {
			_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), syscall.TIOCSPGRP, uintptr(unsafe.Pointer(&foreground)))
		}()
	}
	defer r.Remove("process")
	start, err := processStart(cmd.Process.Pid)
	if err == nil {
		supervisorStart, identityErr := processStart(os.Getpid())
		if identityErr != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Wait()
			return identityErr
		}
		data, _ := json.Marshal(processIdentity{PID: cmd.Process.Pid, Start: start, Supervisor: os.Getpid(), SupervisorStart: supervisorStart})
		err = Install(dir, "process", 0o600, strings.NewReader(string(data)))
	}
	if err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		return fmt.Errorf("register process: %w", err)
	}
	if _, err := r.Stat("cancelled"); err == nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	err = cmd.Wait()
	// A command can leave background children behind. Completion ends this
	// process group too; detached jobs are not a supported execution mode.
	killDescendants(os.Getpid())
	return err
}

func cancelRun(id string) error {
	dir, err := runDirectory(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// A tombstone also cancels a command whose exec request arrived late.
	if err := Install(dir, "cancelled", 0o600, strings.NewReader("cancelled")); err != nil {
		return err
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer r.Close()
	data, err := r.ReadFile("process")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var identity processIdentity
	if err := json.Unmarshal(data, &identity); err != nil {
		return err
	}
	if identity.PID <= 1 {
		return fmt.Errorf("invalid process identity")
	}
	if identity.Supervisor <= 1 {
		return fmt.Errorf("invalid supervisor identity")
	}
	supervisorStart, err := processStart(identity.Supervisor)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if supervisorStart != identity.SupervisorStart {
		return nil
	}
	killDescendants(identity.Supervisor)
	start, err := processStart(identity.PID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if start != identity.Start {
		return nil
	} // Never kill a reused PID.
	if err := syscall.Kill(-identity.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func killDescendants(parent int) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	type child struct {
		pid, ppid int
		start     string
	}
	children := []child{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 {
			continue
		}
		data, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			continue
		}
		end := strings.LastIndex(string(data), ") ")
		if end < 0 {
			continue
		}
		fields := strings.Fields(string(data)[end+2:])
		if len(fields) < 20 {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		children = append(children, child{pid, ppid, fields[19]})
	}
	descendants := map[int]bool{parent: true}
	for changed := true; changed; {
		changed = false
		for _, c := range children {
			if descendants[c.ppid] && !descendants[c.pid] {
				descendants[c.pid] = true
				changed = true
			}
		}
	}
	for _, c := range children {
		if !descendants[c.pid] || c.pid == parent {
			continue
		}
		if start, err := processStart(c.pid); err == nil && start == c.start {
			_ = syscall.Kill(c.pid, syscall.SIGKILL)
		}
	}
}
