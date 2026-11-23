// Command agentterm runs and drives the shared agent terminal.
//
//	agentterm serve    --registry .github/agent-registry.json --data .agentterm
//	agentterm clock-in --server URL --instance claude-1 --branch claude/feature   (GITHUB_TOKEN)
//	agentterm claim    internal/foo
//	agentterm run      -- go test ./internal/foo
//	agentterm record   --exit 0 -- go test ./...   < output.txt
//	agentterm commit   --sha <40hex> --branch claude/feature --pr 12 --path internal/foo
//	agentterm finding  --pr 12 --commit <40hex> --severity major --path a.go --line 9 "text"
//	agentterm status   --finding 42 --status addressed --commit <40hex>
//	agentterm note     "text"   |  heartbeat  |  clock-out  |  release PATH...
//	agentterm tail     [--since N]       (live, reconnecting, verifies each link)
//	agentterm state
//	agentterm verify   [--file log.jsonl --server-key HEX]
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"collaborate/internal/agentregistry"
	"collaborate/internal/agentterm"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "agentterm:", err)
		os.Exit(1)
	}
}

func usage() error {
	return errors.New("usage: agentterm <serve|clock-in|clock-out|heartbeat|claim|release|run|record|commit|finding|status|note|tail|state|verify> [flags]")
}

func defaultSessionPath() string {
	if v := os.Getenv("AGENTTERM_SESSION"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentterm", "session.json")
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usage()
	}
	cmd, args := args[0], args[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	server := fs.String("server", os.Getenv("AGENTTERM_SERVER"), "terminal URL")
	sessionPath := fs.String("session", defaultSessionPath(), "session file (holds the session private key; mode 0600)")
	readToken := fs.String("read-token", os.Getenv("AGENTTERM_READ_TOKEN"), "read token, if the server requires one")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	client := func() (*agentterm.Client, error) {
		c := &agentterm.Client{Server: *server, ReadToken: *readToken, Path: *sessionPath}
		if cmd != "clock-in" && cmd != "tail" && cmd != "state" && cmd != "verify" {
			if err := c.Load(); err != nil {
				return nil, err
			}
		}
		if c.Server == "" {
			return nil, errors.New("--server (or AGENTTERM_SERVER) is required")
		}
		return c, nil
	}
	post := func(kind string, body any) error {
		c, err := client()
		if err != nil {
			return err
		}
		e, err := c.Post(ctx, kind, body)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "#%d %s %s\n", e.Seq, e.Kind, e.Hash)
		return nil
	}

	switch cmd {
	case "serve":
		return serve(ctx, fs, args, stdout)
	case "clock-in":
		instance := fs.String("instance", "", "unique instance name for this agent worker (reuse it to rejoin)")
		branch := fs.String("branch", "", "working branch; must be under your agent's registered prefix, e.g. claude/feature")
		if err := fs.Parse(args); err != nil {
			return err
		}
		token := firstNonEmpty(os.Getenv("AGENTTERM_GITHUB_TOKEN"), os.Getenv("GITHUB_TOKEN"), os.Getenv("GH_TOKEN"))
		if token == "" {
			return errors.New("set GITHUB_TOKEN (or AGENTTERM_GITHUB_TOKEN); it is sent once to the terminal, which asks GitHub who you are")
		}
		c, err := client()
		if err != nil {
			return err
		}
		sf, err := c.ClockIn(ctx, token, *instance, *branch)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "clocked in as %s/%s, session %s (key saved to %s)\n", sf.Agent, *instance, sf.Session, *sessionPath)
		return nil
	case "clock-out", "heartbeat":
		if err := fs.Parse(args); err != nil {
			return err
		}
		kind := map[string]string{"clock-out": agentterm.KindClockOut, "heartbeat": agentterm.KindHeartbeat}[cmd]
		return post(kind, map[string]string{})
	case "claim", "release":
		if err := fs.Parse(args); err != nil {
			return err
		}
		return post(cmd, map[string][]string{"paths": fs.Args()})
	case "run":
		if err := fs.Parse(args); err != nil {
			return err
		}
		return post(agentterm.KindExecRequest, agentterm.ExecRequest{Argv: fs.Args()})
	case "record":
		exit := fs.Int("exit", 0, "exit code of the command you ran")
		where := fs.String("where", "", "where it ran, e.g. 'claude cloud sandbox'")
		if err := fs.Parse(args); err != nil {
			return err
		}
		out, _ := io.ReadAll(io.LimitReader(stdin, 64<<10))
		return post(agentterm.KindRecord, map[string]any{"argv": fs.Args(), "exit": *exit, "output": string(out), "where": *where})
	case "commit":
		sha := fs.String("sha", "", "full commit SHA")
		branch := fs.String("branch", "", "branch the commit is on")
		pr := fs.Int("pr", 0, "pull request number")
		var paths multi
		fs.Var(&paths, "path", "changed path (repeatable); must be covered by your claims")
		if err := fs.Parse(args); err != nil {
			return err
		}
		body := map[string]any{"sha": *sha, "branch": *branch, "pr": *pr}
		if len(paths) > 0 {
			body["paths"] = []string(paths)
		}
		return post(agentterm.KindCommit, body)
	case "finding":
		pr := fs.Int("pr", 0, "pull request number")
		commit := fs.String("commit", "", "exact 40-char SHA you reviewed")
		sev := fs.String("severity", "major", "blocker|major|minor|nit")
		p := fs.String("path", "", "file")
		line := fs.Int("line", 0, "line")
		if err := fs.Parse(args); err != nil {
			return err
		}
		return post(agentterm.KindFinding, map[string]any{"pr": *pr, "commit": *commit, "severity": *sev, "path": *p, "line": *line, "text": strings.Join(fs.Args(), " ")})
	case "status":
		finding := fs.Int64("finding", 0, "finding entry number")
		status := fs.String("status", "", "addressed|disputed|accepted|withdrawn")
		commit := fs.String("commit", "", "fixing commit SHA (required for addressed)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		return post(agentterm.KindFindingStatus, map[string]any{"finding": *finding, "status": *status, "commit": *commit})
	case "note":
		if err := fs.Parse(args); err != nil {
			return err
		}
		return post(agentterm.KindNote, map[string]string{"text": strings.Join(fs.Args(), " ")})
	case "state":
		if err := fs.Parse(args); err != nil {
			return err
		}
		c, err := client()
		if err != nil {
			return err
		}
		var snap json.RawMessage
		if err := c.Get(ctx, "/api/state", &snap); err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, string(snap))
		return err
	case "tail":
		since := fs.Int64("since", -1, "replay entries after this sequence number (-1 = from genesis)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		c, err := client()
		if err != nil {
			return err
		}
		err = c.Tail(ctx, *since, func(e agentterm.Entry) error {
			_, err := fmt.Fprintf(stdout, "#%-5d %s %-14s %-10s %s\n", e.Seq, e.Time[11:19], e.Kind, e.Agent, e.Body)
			return err
		})
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	case "verify":
		file := fs.String("file", "", "verify an exported JSONL log offline instead of a live server")
		keyHex := fs.String("server-key", "", "server public key (hex) to verify a file against")
		if err := fs.Parse(args); err != nil {
			return err
		}
		return verify(ctx, client, *file, *keyHex, stdout)
	}
	return usage()
}

func serve(ctx context.Context, fs *flag.FlagSet, args []string, stdout io.Writer) error {
	addr := fs.String("addr", "127.0.0.1:8787", "listen address (loopback by default; put TLS and auth in front before exposing it)")
	registryPath := fs.String("registry", ".github/agent-registry.json", "agent registry used to authorize clock-ins")
	data := fs.String("data", ".agentterm", "directory for log.jsonl and the server key (never commit it)")
	githubAPI := fs.String("github-api", "https://api.github.com", "GitHub API base URL")
	ttl := fs.Duration("ttl", 2*time.Hour, "a session expires after this long without a signed request")
	allowExec := fs.Bool("allow-exec", false, "let agents run allowlisted read-only/verification commands in --workdir")
	workdir := fs.String("workdir", ".", "workspace for --allow-exec")
	if err := fs.Parse(args); err != nil {
		return err
	}
	reg, err := agentregistry.Load(*registryPath)
	if err != nil {
		return err
	}
	key, err := agentterm.LoadOrCreateKey(filepath.Join(*data, "server.key"))
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(*workdir)
	if err != nil {
		return err
	}
	policy := agentterm.ExecPolicy{Enabled: *allowExec, Dir: abs, Timeout: 10 * time.Minute}
	term, err := agentterm.Open(filepath.Join(*data, "log.jsonl"), key, agentterm.NewState(reg, *ttl, policy), nil)
	if err != nil {
		return err
	}
	defer term.Close()
	srv := &agentterm.Server{T: term, Registry: reg, Auth: agentterm.GitHubAuth{APIURL: *githubAPI},
		ReadToken: os.Getenv("AGENTTERM_READ_TOKEN"), Exec: policy}
	defer srv.Shutdown()
	go func() {
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				term.ExpireStale()
			}
		}
	}()
	hs := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(shutdown)
	}()
	fmt.Fprintf(stdout, "agent terminal on http://%s  server key %s  exec=%v\n", *addr, hex.EncodeToString(term.PublicKey()), *allowExec)
	if err := hs.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func verify(ctx context.Context, client func() (*agentterm.Client, error), file, keyHex string, stdout io.Writer) error {
	var entries []agentterm.Entry
	var key []byte
	var err error
	if file != "" {
		key, err = hex.DecodeString(keyHex)
		if err != nil || len(key) != ed25519.PublicKeySize {
			return errors.New("--server-key must be the terminal's 64-hex-character public key")
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var e agentterm.Entry
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				return fmt.Errorf("line %d: %w", i+1, err)
			}
			entries = append(entries, e)
		}
	} else {
		c, err := client()
		if err != nil {
			return err
		}
		var info struct {
			ServerKey string `json:"server_key"`
		}
		if err := c.Get(ctx, "/api/info", &info); err != nil {
			return err
		}
		if keyHex != "" && !strings.EqualFold(keyHex, info.ServerKey) {
			return errors.New("server key does not match --server-key")
		}
		key, _ = hex.DecodeString(info.ServerKey)
		for since := int64(-1); ; {
			var page []agentterm.Entry
			if err := c.Get(ctx, fmt.Sprintf("/api/log?since=%d", since), &page); err != nil {
				return err
			}
			if len(page) == 0 {
				break
			}
			entries = append(entries, page...)
			since = page[len(page)-1].Seq
		}
	}
	rep, err := agentterm.VerifyChain(entries, key)
	if err != nil {
		return fmt.Errorf("VERIFICATION FAILED: %w", err)
	}
	fmt.Fprintf(stdout, "OK: %d entries, %d agent-signed, head %s\n", rep.Entries, rep.AgentSigned, rep.Head)
	return nil
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
