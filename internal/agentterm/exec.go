package agentterm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ExecPolicy controls server-side command execution. It is off by default.
// When enabled, commands run without a shell, from an allowlist of
// read-only and verification commands, in one workspace directory, with a
// scrubbed environment. No git command that writes, pushes, merges, rebases
// or resets is allowed, so the terminal can never change a remote branch.
type ExecPolicy struct {
	Enabled bool          `json:"enabled"`
	Dir     string        `json:"-"`
	Timeout time.Duration `json:"timeout"`
}

// ExecRequest is the body an agent signs to ask the server to run argv.
type ExecRequest struct {
	Argv []string `json:"argv"`
}

// ExecResult is the server-authored body that answers an exec_request.
type ExecResult struct {
	Request    int64  `json:"request"`
	Exit       int    `json:"exit"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	Truncated  bool   `json:"truncated"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
	Verified   string `json:"verified"`
}

var allowed = map[string]map[string]bool{
	"go":      {"test": true, "vet": true, "build": true, "list": true, "version": true},
	"gofmt":   {"-l": true, "-d": true},
	"git":     {"status": true, "diff": true, "log": true, "show": true, "rev-parse": true, "ls-files": true, "blame": true},
	"python3": {"-m": true},
}

var pythonModules = map[string]bool{"unittest": true, "py_compile": true}

// Flags that would write files outside the workspace, run another program,
// or redirect configuration.
var deniedFlagPrefixes = []string{"-o", "-exec", "-toolexec", "-C", "-modfile", "-overlay", "-vettool", "-ldflags", "-pkgdir", "--output", "--ext-diff", "--textconv", "-w", "--exec", "--upload-pack", "--config"}

// Check reports whether argv may run under the policy.
func (p ExecPolicy) Check(argv []string) error {
	if !p.Enabled {
		return errors.New("exec: server-side execution is disabled on this terminal (use 'record' to log a command you ran yourself)")
	}
	if len(argv) < 2 || len(argv) > 64 {
		return errors.New("exec: argv must have a program, a subcommand and at most 64 arguments")
	}
	subs, ok := allowed[argv[0]]
	if !ok {
		return fmt.Errorf("exec: program %q is not on the allowlist (go, gofmt, git, python3)", argv[0])
	}
	if !subs[argv[1]] {
		return fmt.Errorf("exec: %s %s is not allowed; read-only and verification subcommands only", argv[0], argv[1])
	}
	if argv[0] == "python3" && (len(argv) < 3 || !pythonModules[argv[2]]) {
		return errors.New("exec: python3 is limited to -m unittest and -m py_compile")
	}
	for _, a := range argv[1:] {
		if len(a) > 1024 || strings.ContainsRune(a, 0) {
			return errors.New("exec: argument too long or contains NUL")
		}
		if a == "-l" || a == "-d" { // gofmt list/diff are the subcommand itself
			continue
		}
		for _, d := range deniedFlagPrefixes {
			if a == d || strings.HasPrefix(a, d+"=") || (strings.HasPrefix(a, d) && len(d) == 2 && !strings.HasPrefix(a, "--")) {
				return fmt.Errorf("exec: flag %q is not allowed", a)
			}
		}
		if !strings.HasPrefix(a, "-") {
			if filepath.IsAbs(a) || a == ".." || strings.HasPrefix(a, "../") || strings.Contains(a, "/../") {
				return fmt.Errorf("exec: argument %q leaves the workspace", a)
			}
		}
	}
	return nil
}

// Run executes argv under the policy. It never uses a shell.
func (p ExecPolicy) Run(ctx context.Context, argv []string) ExecResult {
	res := ExecResult{Verified: "server-executed"}
	if err := p.Check(argv); err != nil {
		res.Exit, res.Error = -1, err.Error()
		return res
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = p.Dir
	cmd.Env = scrubbedEnv()
	var out, errOut limitedBuffer
	out.limit, errOut.limit = maxOutput/2, maxOutput/2
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	res.DurationMS = time.Since(start).Milliseconds()
	res.Stdout, res.Stderr = out.String(), errOut.String()
	res.Truncated = out.truncated || errOut.truncated
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		res.Exit = exitErr.ExitCode()
	default:
		res.Exit, res.Error = -1, err.Error()
	}
	if ctx.Err() == context.DeadlineExceeded {
		res.Error = "timed out after " + timeout.String()
	}
	return res
}

// scrubbedEnv passes only what toolchains need. Tokens and credentials in the
// server's environment are never handed to executed commands.
func scrubbedEnv() []string {
	env := []string{"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GOFLAGS=-mod=readonly"}
	// Toolchain selection settings pass through unchanged, so commands use the
	// Go version go.mod requires (checksum-verified download) exactly as CI does.
	for _, k := range []string{"PATH", "HOME", "GOCACHE", "GOMODCACHE", "GOPATH", "GOROOT", "GOTOOLCHAIN", "GOPROXY", "GOSUMDB", "CGO_ENABLED", "TMPDIR", "LANG",
		"HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "SSL_CERT_FILE"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

type limitedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.Len(); room < len(p) {
		if room > 0 {
			b.Buffer.Write(p[:room])
		}
		b.truncated = true
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

func (b *limitedBuffer) String() string { return strings.ToValidUTF8(b.Buffer.String(), "�") }
