package main

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DeontewattsV1/Collaborate.asciidoc/internal/collab"
)

type synchronizedHash struct {
	mu   sync.Mutex
	hash hash.Hash
}

func (s *synchronizedHash) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hash.Write(p)
}

func usage() {
	fmt.Fprint(os.Stderr, `collab v0.1 — multi-agent terminal coordination with broker-signed receipts

Commands:
  serve     Run local SQLite-backed broker (COLLAB_TOKEN required)
  register  Register an agent
  say       Send a live message to another agent or channel
  claim     Obtain an exclusive path-prefix lease
  renew     Extend an active lease before its expiration
  release   Release a lease owned by your agent
  receipt   Record a Git commit receipt against held leases
  gate      Run a local test command and report its exit status and output hash
  events    Read ledger events as JSON
  watch     Subscribe to live events using SSE
  verify    Verify broker signatures, hash chain, and optional pinned head
  report    Render collaborate.adoc from the SQLite ledger
  worktree  Create isolated Git worktree and branch
  mcp       Run model-facing MCP stdio tool adapter

Set COLLAB_URL (default http://127.0.0.1:8787) and COLLAB_TOKEN for clients.
Example: collab serve -db .collab/state.sqlite -key .collab/broker.key
`)
}
func fs(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(os.Stderr)
	return f
}
func clientURL() string {
	if u := os.Getenv("COLLAB_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://127.0.0.1:8787"
}
func request(method, path string, data any) ([]byte, error) {
	var body io.Reader
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, clientURL()+path, body)
	if err != nil {
		return nil, err
	}
	if data != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token := os.Getenv("COLLAB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	c := http.Client{Timeout: 20 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("broker HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return b, nil
}
func post(path string, v any) error {
	b, err := request("POST", path, v)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(b)
	return err
}
func run(args []string) error {
	if len(args) < 1 {
		usage()
		return errors.New("missing command")
	}
	switch args[0] {
	case "mcp":
		return serveMCP(os.Stdin, os.Stdout)
	case "help", "-h", "--help":
		usage()
		return nil
	case "serve":
		f := fs("serve")
		db := f.String("db", ".collab/state.sqlite", "SQLite database")
		key := f.String("key", ".collab/broker.key", "broker private key")
		pub := f.String("pub", ".collab/broker.pub", "pinned public key output")
		listen := f.String("listen", "127.0.0.1:8787", "loopback host:port")
		unauth := f.Bool("unsafe-local-no-auth", false, "allow unauthenticated local testing only")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if err := collab.SafeListenAddr(*listen); err != nil {
			return err
		}
		token := os.Getenv("COLLAB_TOKEN")
		if token == "" && !*unauth {
			return errors.New("COLLAB_TOKEN required; use -unsafe-local-no-auth only for isolated testing")
		}
		store, err := collab.OpenStore(*db, *key)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := os.MkdirAll(filepath.Dir(*pub), 0700); err != nil {
			return err
		}
		if b, err := os.ReadFile(*pub); err == nil {
			if !bytes.Equal(b, store.PublicKey()) {
				return errors.New("pinned broker public key differs: refusing startup")
			}
		} else if errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(*pub, store.PublicKey(), 0644); err != nil {
				return err
			}
		} else {
			return err
		}
		l, err := net.Listen("tcp", *listen)
		if err != nil {
			return err
		}
		defer l.Close()
		fmt.Printf("collab broker listening on %s (SQLite %s)\n", *listen, *db)
		srv := &http.Server{Handler: collab.NewServer(store, token).Handler(), ReadHeaderTimeout: 5 * time.Second}
		return srv.Serve(l)
	case "register":
		f := fs("register")
		a := f.String("agent", "", "agent name")
		m := f.String("model", "", "model identifier (self-declared)")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		return post("/v1/register", collab.RegisterInput{Agent: *a, Model: *m})
	case "say":
		f := fs("say")
		a := f.String("agent", "", "agent")
		to := f.String("to", "all", "recipient or channel")
		body := f.String("body", "", "message")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		return post("/v1/message", collab.MessageInput{Agent: *a, To: *to, Body: *body})
	case "claim":
		f := fs("claim")
		a := f.String("agent", "", "agent")
		p := f.String("path", "", "file/directory path relative to repo")
		ttl := f.Int64("ttl", 60, "lease seconds (5-3600)")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		return post("/v1/lease", collab.LeaseInput{Agent: *a, Path: *p, TTLSeconds: *ttl})
	case "renew":
		f := fs("renew")
		a := f.String("agent", "", "agent")
		id := f.String("lease", "", "lease id")
		ttl := f.Int64("ttl", 60, "new lease duration in seconds")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		return post("/v1/renew", collab.RenewInput{Agent: *a, LeaseID: *id, TTLSeconds: *ttl})
	case "release":
		f := fs("release")
		a := f.String("agent", "", "agent")
		id := f.String("lease", "", "lease id")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		return post("/v1/release", collab.ReleaseInput{Agent: *a, LeaseID: *id})
	case "receipt":
		f := fs("receipt")
		a := f.String("agent", "", "agent")
		commit := f.String("commit", "", "40/64-char Git commit hash")
		paths := f.String("paths", "", "comma-separated affected paths (optional; checked against Git)")
		repo := f.String("repo", ".", "local Git repo used to verify commit and changed paths")
		summary := f.String("summary", "", "change summary")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		changed, err := gitChangedPaths(*repo, *commit)
		if err != nil {
			return err
		}
		list := []string{}
		if *paths != "" {
			for _, p := range strings.Split(*paths, ",") {
				list = append(list, strings.TrimSpace(p))
			}
		}
		if *paths == "" {
			list = changed
		} else if !samePaths(changed, list) {
			return errors.New("receipt paths differ from the actual Git commit diff")
		}
		return post("/v1/receipt", collab.ReceiptInput{Agent: *a, Commit: *commit, Paths: list, Summary: *summary})
	case "gate":
		f := fs("gate")
		a := f.String("agent", "", "agent")
		commit := f.String("commit", "", "optional associated Git commit")
		cwd := f.String("workdir", ".", "directory to run command in")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		argv := f.Args()
		if len(argv) > 0 && argv[0] == "--" {
			argv = argv[1:]
		}
		if len(argv) == 0 {
			return errors.New("gate needs command after --")
		}
		c := exec.Command(argv[0], argv[1:]...)
		c.Dir = *cwd
		h := sha256.New()
		locked := &synchronizedHash{hash: h}
		c.Stdout = io.MultiWriter(os.Stdout, locked)
		c.Stderr = io.MultiWriter(os.Stderr, locked)
		err := c.Run()
		exit := 0
		if err != nil {
			var ex *exec.ExitError
			if errors.As(err, &ex) {
				exit = ex.ExitCode()
			} else {
				return err
			}
		}
		body := collab.GateInput{Agent: *a, Commit: *commit, Worktree: *cwd, Command: argv, ExitCode: exit, OutputSHA256: hex.EncodeToString(h.Sum(nil))}
		if e := post("/v1/gate", body); e != nil {
			return e
		}
		if exit != 0 {
			return fmt.Errorf("test gate failed: exit %d (recorded)", exit)
		}
		return nil
	case "events":
		f := fs("events")
		after := f.Int64("after", 0, "last seen sequence number")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		b, err := request("GET", fmt.Sprintf("/v1/events?after=%d", *after), nil)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	case "watch":
		f := fs("watch")
		after := f.Int64("after", 0, "last seen sequence number")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		req, err := http.NewRequest("GET", clientURL()+fmt.Sprintf("/v1/watch?after=%d", *after), nil)
		if err != nil {
			return err
		}
		if t := os.Getenv("COLLAB_TOKEN"); t != "" {
			req.Header.Set("Authorization", "Bearer "+t)
		}
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("watch broker status: %s", resp.Status)
		}
		scan := bufio.NewScanner(resp.Body)
		scan.Buffer(make([]byte, 4096), 2<<20)
		for scan.Scan() {
			line := scan.Text()
			if strings.HasPrefix(line, "data: ") {
				fmt.Println(strings.TrimPrefix(line, "data: "))
			}
		}
		return scan.Err()
	case "verify":
		f := fs("verify")
		db := f.String("db", ".collab/state.sqlite", "database path")
		pub := f.String("pub", ".collab/broker.pub", "pinned broker public key")
		head := f.String("expected-head", "", "optional externally anchored final hash")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		key, err := os.ReadFile(*pub)
		if err != nil {
			return err
		}
		if len(key) != ed25519.PublicKeySize {
			return errors.New("invalid public key file")
		}
		n, last, err := collab.Verify(*db, ed25519.PublicKey(key), *head)
		if err != nil {
			return err
		}
		fmt.Printf("PASS: %d broker-signed events; ledger head %s\n", n, last)
		return nil
	case "report":
		f := fs("report")
		db := f.String("db", ".collab/state.sqlite", "database")
		key := f.String("key", ".collab/broker.key", "broker key")
		out := f.String("out", "collaborate.adoc", "AsciiDoc report file")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		store, err := collab.OpenStore(*db, *key)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := collab.WriteReport(store, *out); err != nil {
			return err
		}
		fmt.Println("wrote", *out)
		return nil
	case "worktree":
		f := fs("worktree")
		repo := f.String("repo", ".", "Git repository")
		dest := f.String("path", "", "isolated worktree destination")
		branch := f.String("branch", "", "unique branch, e.g. collab/session/agent")
		base := f.String("base", "HEAD", "base ref")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if *dest == "" || *branch == "" {
			return errors.New("worktree requires -path and -branch")
		}
		c := exec.Command("git", "-C", *repo, "worktree", "add", "-b", *branch, "--", *dest, *base)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}
