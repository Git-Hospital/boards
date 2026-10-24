// Package execx runs commands with a deadline and an environment boards
// chose, never the one it was started with (architecture §7).
package execx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Grace is how long a process gets between SIGTERM and SIGKILL. One HolmesGPT
// run ignored TERM and held the old harness for seven hours.
const Grace = 20 * time.Second

// Cmd is one command to run.
type Cmd struct {
	Argv  []string
	Env   []string // the complete environment; nothing is inherited
	Dir   string
	Stdin string
	// Stdout and Stderr, when set, receive the output instead of the
	// buffers returned by Run.
	Stdout, Stderr *os.File
}

// Result is what a finished command produced.
type Result struct {
	Stdout, Stderr string
	ExitCode       int
	TimedOut       bool
}

// Run runs c until it exits or ctx ends. When ctx ends, the process group gets
// SIGTERM, then SIGKILL after Grace. A non-zero exit is reported in
// Result.ExitCode, not as an error; err is for failing to run at all.
func Run(ctx context.Context, c Cmd) (Result, error) {
	if len(c.Argv) == 0 {
		return Result{}, errors.New("execx: empty command")
	}
	cmd := exec.Command(c.Argv[0], c.Argv[1:]...)
	cmd.Env = c.Env
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	cmd.Dir = c.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if c.Stdin != "" {
		cmd.Stdin = strings.NewReader(c.Stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if c.Stdout != nil {
		cmd.Stdout = c.Stdout
	}
	if c.Stderr != nil {
		cmd.Stderr = c.Stderr
	}

	if err := cmd.Start(); err != nil {
		return Result{}, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var err error
	timedOut := false
	select {
	case err = <-done:
	case <-ctx.Done():
		timedOut = true
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case err = <-done:
		case <-time.After(Grace):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			err = <-done
		}
	}

	res := Result{Stdout: out.String(), Stderr: errb.String(), TimedOut: timedOut}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		return res, err
	}
	return res, nil
}

// BaseEnv is the minimum a command needs to find its tools. PATH is the one
// variable taken from the caller: without it, kubectl and the tools under
// test cannot be found.
func BaseEnv(home string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"LANG=C",
		"LC_ALL=C",
	}
}
