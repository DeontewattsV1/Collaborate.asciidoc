package main

import (
	"bufio"
	"bytes"
	"collaborate/internal/telemetry"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"collaborate/internal/adapter"
	"collaborate/internal/broker"
	"collaborate/internal/client"
	"collaborate/internal/mcpserver"
	"collaborate/internal/repoops"
	"collaborate/internal/store"
)

const defaultURL = "http://127.0.0.1:8787"

func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "collaborate:", err)
		os.Exit(1)
	}
}
func usage() {
	fmt.Print(`collaborate — coordinate agents with signed receipts

Commands:
	serve --db FILE [--addr 127.0.0.1:8787] [--lease-ttl 90s] [--gates-file FILE] [--export-dir DIR] [--tls-cert FILE --tls-key FILE --client-ca FILE --admin-token-file FILE]
	init --session ID [--repo DIR] [--url URL] [--trunk BRANCH] [--ca FILE --client-cert FILE --client-key FILE --admin-token-file FILE]
	register --agent ID --model MODEL [--repo DIR] [--admin-token-file FILE]
  claim --intent TEXT PATH...
  write PATH                 read content from stdin and write through the lease-checking broker
  release PATH...
  renew | say MESSAGE...
  spawn [--agent ID] [--branch BRANCH]
  commit -m MESSAGE | receipt
  submit | queue | review --queue-id N --verdict approve|request_changes | integrate --queue-id N
  rotate-key | revoke-key --version N
	status | verify | render | export --out FILE [--admin-token-file FILE]
		adapter list | adapter run --name echo|openai-compatible --prompt TEXT
		mcp --repo DIR --agent ID          stdio MCP coordination server
		mcp [--repo DIR] --agent ID        stdio MCP server for one agent identity

Set COLLAB_AGENT to select the registered signing identity. Remote serving requires mTLS.
openai-compatible reads COLLAB_MODEL_BASE_URL, COLLAB_MODEL, and COLLAB_MODEL_API_KEY (or OPENAI_API_KEY) from the environment.
`)
}
func abs(p string) string { a, e := filepath.Abs(p); fatal(e); return a }
func rootOrCwd(explicit string) (string, error) {
	if explicit != "" {
		return abs(explicit), nil
	}
	cwd, e := os.Getwd()
	if e != nil {
		return "", e
	}
	return client.FindRepo(cwd)
}
func httpJSONWithClient(h *http.Client, method, url string, body any, out any) error {
	return httpJSONWithHeaders(h, method, url, body, out, nil)
}
func httpJSONWithHeaders(h *http.Client, method, url string, body any, out any, headers map[string]string) error {
	b, e := json.Marshal(body)
	if e != nil {
		return e
	}
	req, e := http.NewRequest(method, url, strings.NewReader(string(b)))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if h == nil {
		h = http.DefaultClient
	}
	resp, e := h.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if e != nil {
		return e
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}
func httpJSON(method, url string, body any, out any) error {
	return httpJSONWithClient(http.DefaultClient, method, url, body, out)
}
func config(repo string) (client.Config, error) { return client.LoadConfig(repo) }
func getClient(repo, agentOverride string) (client.Config, *client.Client, error) {
	cfg, e := config(repo)
	if e != nil {
		return cfg, nil, e
	}
	agent := agentOverride
	if agent == "" {
		agent = os.Getenv("COLLAB_AGENT")
	}
	if agent == "" {
		return cfg, nil, errors.New("set COLLAB_AGENT or pass --agent")
	}
	key, e := client.LoadKey(filepath.Join(repo, ".collab", "keys", agent+".key"))
	if e != nil {
		return cfg, nil, e
	}
	cli, e := client.NewConfigured(cfg, agent, key)
	return cfg, cli, e
}
func repoAndConfig(explicit string) (string, client.Config, error) {
	r, e := rootOrCwd(explicit)
	if e != nil {
		return "", client.Config{}, e
	}
	c, e := config(r)
	return r, c, e
}
func openProject(explicit, agent string) (string, client.Config, *client.Client, error) {
	r, c, e := repoAndConfig(explicit)
	if e != nil {
		return "", c, nil, e
	}
	_, cli, e := getClient(r, agent)
	return r, c, cli, e
}
func currentDir() string { p, e := os.Getwd(); fatal(e); return p }
func runGit(dir string, args ...string) ([]byte, error) {
	c := exec.Command("git", args...)
	c.Dir = dir
	return c.CombinedOutput()
}
func gitOut(dir string, args ...string) (string, error) {
	b, e := runGit(dir, args...)
	return strings.TrimSpace(string(b)), e
}
func mustAgentFlag(fs *flag.FlagSet) *string { return fs.String("agent", "", "agent ID override") }
func clientHTTP(cfg client.Config) (*http.Client, error) {
	c, e := client.NewConfigured(cfg, "", nil)
	if e != nil {
		return nil, e
	}
	return c.HTTP, nil
}
func adminToken(file string) (string, error) {
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		token := strings.TrimSpace(string(b))
		if token == "" {
			return "", errors.New("operator token file is empty")
		}
		return token, nil
	}
	return strings.TrimSpace(os.Getenv("COLLAB_ADMIN_TOKEN")), nil
}

func verifyExportPrefix(filename string, all []store.Event) (int, error) {
	f, err := os.Open(filename)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	n := 0
	for sc.Scan() {
		var e store.Event
		dec := json.NewDecoder(bytes.NewReader(sc.Bytes()))
		dec.DisallowUnknownFields()
		if err = dec.Decode(&e); err != nil {
			return 0, fmt.Errorf("invalid existing export line %d: %w", n+1, err)
		}
		var extra any
		if err = dec.Decode(&extra); err != io.EOF {
			return 0, fmt.Errorf("invalid existing export line %d: trailing JSON data", n+1)
		}
		if n >= len(all) {
			return 0, fmt.Errorf("existing export is not a valid prefix of the server ledger at line %d", n+1)
		}
		want, _ := json.Marshal(all[n])
		got, _ := json.Marshal(e)
		if !bytes.Equal(want, got) {
			return 0, fmt.Errorf("existing export diverges from the server ledger at line %d", n+1)
		}
		n++
	}
	if err = sc.Err(); err != nil {
		return 0, err
	}
	return n, nil
}
func saveAppendOnlyExport(filename string, data []byte) error {
	var events []store.Event
	if err := json.Unmarshal(data, &events); err != nil {
		return fmt.Errorf("decode ledger export: %w", err)
	}
	n, err := verifyExportPrefix(filename, events)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range events[n:] {
		if err = enc.Encode(e); err != nil {
			return err
		}
	}
	return f.Sync()
}
func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	args := os.Args[2:]
	switch os.Args[1] {
	case "help", "-h", "--help":
		usage()
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		db := fs.String("db", "collaborate.sqlite", "SQLite ledger path")
		addr := fs.String("addr", "127.0.0.1:8787", "listen address")
		ttl := fs.Duration("lease-ttl", 90*time.Second, "lease lifetime")
		gatesPath := fs.String("gates-file", "", "operator-owned JSON gate map keyed by session ID")
		exportDir := fs.String("export-dir", "", "append-only JSONL ledger export directory")
		cert := fs.String("tls-cert", "", "TLS server certificate")
		key := fs.String("tls-key", "", "TLS server private key")
		clientCA := fs.String("client-ca", "", "CA bundle required for remote mTLS clients")
		adminTokenFile := fs.String("admin-token-file", "", "operator token file required for remote bootstrap")
		_ = fs.Parse(args)
		if d := filepath.Dir(*db); d != "." {
			fatal(os.MkdirAll(d, 0700))
		}
		s, e := store.Open(*db)
		fatal(e)
		defer s.Close()
		gates := map[string][]repoops.Gate{}
		if *gatesPath != "" {
			b, e := os.ReadFile(*gatesPath)
			fatal(e)
			fatal(json.Unmarshal(b, &gates))
		}
		token, e := adminToken(*adminTokenFile)
		fatal(e)
		otelShutdown, e := telemetry.Setup(context.Background(), "collaborate-broker")
		fatal(e)
		defer otelShutdown(context.Background())
		go func() {
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
			<-sig
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = otelShutdown(ctx)
			os.Exit(0)
		}()
		srv := &broker.Server{Store: s, LeaseTTL: *ttl, Gates: gates, ExportDir: *exportDir, AdminToken: token}
		fmt.Printf("collaborate broker listening on %s (lease TTL %s)\n", *addr, ttl.String())
		fatal(srv.ListenTLS(*addr, *cert, *key, *clientCA))
	case "init":
		fs := flag.NewFlagSet("init", flag.ExitOnError)
		session := fs.String("session", "", "session ID")
		repo := fs.String("repo", ".", "Git repository directory")
		serverURL := fs.String("url", defaultURL, "broker URL")
		trunk := fs.String("trunk", "", "integration branch; defaults to collab/<session>/trunk")
		ca := fs.String("ca", "", "trusted server CA PEM file")
		clientCert := fs.String("client-cert", "", "mTLS client certificate")
		clientKey := fs.String("client-key", "", "mTLS client private key")
		adminTokenFile := fs.String("admin-token-file", "", "operator token file; or use COLLAB_ADMIN_TOKEN")
		_ = fs.Parse(args)
		if *session == "" {
			fatal(errors.New("--session is required"))
		}
		r := abs(*repo)
		if _, e := os.Stat(filepath.Join(r, ".git")); e != nil {
			fatal(fmt.Errorf("%s is not a Git worktree", r))
		}
		branch := *trunk
		if branch == "" {
			branch = "collab/" + *session + "/trunk"
		}
		cfg := client.Config{URL: *serverURL, Session: *session, Repo: r, TrunkBranch: branch, CAFile: *ca, ClientCertFile: *clientCert, ClientKeyFile: *clientKey}
		h, e := clientHTTP(cfg)
		fatal(e)
		token, e := adminToken(*adminTokenFile)
		fatal(e)
		headers := map[string]string{}
		if token != "" {
			headers["X-Collab-Admin-Token"] = token
		}
		var response map[string]any
		e = httpJSONWithHeaders(h, "POST", strings.TrimRight(cfg.URL, "/")+"/v1/sessions", map[string]string{"id": cfg.Session, "repo": cfg.Repo, "trunk": cfg.TrunkBranch}, &response, headers)
		fatal(e)
		fatal(client.SaveConfig(r, cfg))
		fmt.Printf("initialized session %s (trunk %s) in %s\n", cfg.Session, cfg.TrunkBranch, r)
	case "register":
		fs := flag.NewFlagSet("register", flag.ExitOnError)
		agent := fs.String("agent", "", "agent ID")
		model := fs.String("model", "", "model name/version")
		repo := fs.String("repo", "", "project directory")
		adminTokenFile := fs.String("admin-token-file", "", "operator token file; or use COLLAB_ADMIN_TOKEN")
		_ = fs.Parse(args)
		if *agent == "" || *model == "" {
			fatal(errors.New("--agent and --model are required"))
		}
		r, cfg, e := repoAndConfig(*repo)
		fatal(e)
		keyPath := filepath.Join(r, ".collab", "keys", *agent+".key")
		if _, err := os.Stat(keyPath); err == nil {
			fatal(fmt.Errorf("key already exists for agent %q; refusing to replace it", *agent))
		} else if !errors.Is(err, os.ErrNotExist) {
			fatal(err)
		}
		pub, priv, e := client.GenerateKey()
		fatal(e)
		h, e := clientHTTP(cfg)
		fatal(e)
		token, e := adminToken(*adminTokenFile)
		fatal(e)
		headers := map[string]string{}
		if token != "" {
			headers["X-Collab-Admin-Token"] = token
		}
		var out map[string]any
		e = httpJSONWithHeaders(h, "POST", strings.TrimRight(cfg.URL, "/")+"/v1/agents?session="+url.QueryEscape(cfg.Session), map[string]any{"agent": *agent, "model": *model, "pubkey": pub}, &out, headers)
		fatal(e)
		fatal(client.SaveKey(keyPath, priv))
		fmt.Printf("registered %s (%s); private key saved locally\n", *agent, *model)
	case "claim":
		fs := flag.NewFlagSet("claim", flag.ExitOnError)
		intent := fs.String("intent", "", "why paths are needed")
		agent := mustAgentFlag(fs)
		_ = fs.Parse(args)
		_, _, cli, e := openProject("", *agent)
		fatal(e)
		if *intent == "" {
			fatal(errors.New("--intent is required"))
		}
		b, e := cli.Do("POST", "/v1/claim", map[string]any{"intent": *intent, "paths": fs.Args()})
		fatal(e)
		fmt.Println(string(b))
	case "write":
		fs := flag.NewFlagSet("write", flag.ExitOnError)
		agent := mustAgentFlag(fs)
		_ = fs.Parse(args)
		if fs.NArg() != 1 {
			fatal(errors.New("usage: collaborate write PATH < CONTENT"))
		}
		_, _, cli, e := openProject("", *agent)
		fatal(e)
		content, e := io.ReadAll(io.LimitReader(os.Stdin, 16<<20))
		fatal(e)
		b, e := cli.Do("POST", "/v1/write", map[string]any{"path": fs.Arg(0), "content": content})
		fatal(e)
		fmt.Println(string(b))
	case "release":
		fs := flag.NewFlagSet("release", flag.ExitOnError)
		agent := mustAgentFlag(fs)
		_ = fs.Parse(args)
		_, _, cli, e := openProject("", *agent)
		fatal(e)
		b, e := cli.Do("POST", "/v1/release", map[string]any{"paths": fs.Args()})
		fatal(e)
		fmt.Println(string(b))
	case "renew":
		fs := flag.NewFlagSet("renew", flag.ExitOnError)
		agent := mustAgentFlag(fs)
		_ = fs.Parse(args)
		_, _, cli, e := openProject("", *agent)
		fatal(e)
		b, e := cli.Do("POST", "/v1/renew", map[string]any{})
		fatal(e)
		fmt.Println(string(b))
	case "say":
		fs := flag.NewFlagSet("say", flag.ExitOnError)
		agent := mustAgentFlag(fs)
		_ = fs.Parse(args)
		_, _, cli, e := openProject("", *agent)
		fatal(e)
		if fs.NArg() == 0 {
			fatal(errors.New("message required"))
		}
		b, e := cli.Do("POST", "/v1/say", map[string]string{"body": strings.Join(fs.Args(), " ")})
		fatal(e)
		fmt.Println(string(b))
	case "spawn":
		fs := flag.NewFlagSet("spawn", flag.ExitOnError)
		agent := mustAgentFlag(fs)
		branch := fs.String("branch", "", "agent branch")
		_ = fs.Parse(args)
		_, cfg, cli, e := openProject("", *agent)
		fatal(e)
		body := map[string]string{}
		if *branch != "" {
			body["branch"] = *branch
		}
		b, e := cli.Do("POST", "/v1/worktree", body)
		fatal(e)
		fmt.Println(string(b))
		_ = cfg
	case "commit", "receipt":
		fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
		message := fs.String("m", "", "commit message")
		agent := mustAgentFlag(fs)
		_ = fs.Parse(args)
		_, _, cli, e := openProject("", *agent)
		fatal(e)
		wd := currentDir()
		if os.Args[1] == "commit" {
			if *message == "" {
				fatal(errors.New("-m message is required"))
			}
			out, err := runGit(wd, "commit", "-m", *message)
			if err != nil {
				fatal(fmt.Errorf("git commit: %s", strings.TrimSpace(string(out))))
			}
		}
		sha, e := gitOut(wd, "rev-parse", "HEAD")
		fatal(e)
		b, e := cli.Do("POST", "/v1/receipt", map[string]string{"commit": sha})
		fatal(e)
		fmt.Printf("server-verified receipt stored for %s: %s\n", sha[:12], strings.TrimSpace(string(b)))
	case "submit":
		fs := flag.NewFlagSet("submit", flag.ExitOnError)
		agent := mustAgentFlag(fs)
		_ = fs.Parse(args)
		wd := currentDir()
		sha, e := gitOut(wd, "rev-parse", "HEAD")
		fatal(e)
		_, _, cli, e := openProject("", *agent)
		fatal(e)
		b, e := cli.Do("POST", "/v1/submit", map[string]string{"commit": sha})
		fatal(e)
		fmt.Println(string(b))
	case "review":
		fs := flag.NewFlagSet("review", flag.ExitOnError)
		agent := mustAgentFlag(fs)
		id := fs.Int64("queue-id", 0, "queue item ID")
		verdict := fs.String("verdict", "", "approve or request_changes")
		reason := fs.String("reason", "", "review note")
		_ = fs.Parse(args)
		_, _, cli, e := openProject("", *agent)
		fatal(e)
		b, e := cli.Do("POST", "/v1/review", map[string]any{"queue_id": *id, "verdict": *verdict, "reason": *reason})
		fatal(e)
		fmt.Println(string(b))
	case "integrate":
		fs := flag.NewFlagSet("integrate", flag.ExitOnError)
		agent := mustAgentFlag(fs)
		id := fs.Int64("queue-id", 0, "approved queue item ID")
		_ = fs.Parse(args)
		_, _, cli, e := openProject("", *agent)
		fatal(e)
		b, e := cli.Do("POST", "/v1/integrate", map[string]int64{"queue_id": *id})
		fatal(e)
		fmt.Println(string(b))
	case "queue":
		fs := flag.NewFlagSet("queue", flag.ExitOnError)
		agent := mustAgentFlag(fs)
		_ = fs.Parse(args)
		_, _, cli, e := openProject("", *agent)
		fatal(e)
		b, e := cli.Do("GET", "/v1/queue?session="+url.QueryEscape(cli.Session), nil)
		fatal(e)
		fmt.Println(string(b))
	case "rotate-key":
		fs := flag.NewFlagSet("rotate-key", flag.ExitOnError)
		agent := mustAgentFlag(fs)
		_ = fs.Parse(args)
		r, _, cli, e := openProject("", *agent)
		fatal(e)
		keyPath := filepath.Join(r, ".collab", "keys", cli.Agent+".key")
		old, e := os.ReadFile(keyPath)
		fatal(e)
		pub, priv, e := client.GenerateKey()
		fatal(e)
		next := keyPath + ".next"
		fatal(client.SaveKey(next, priv))
		type answer struct {
			Version int `json:"version"`
		}
		data, e := cli.Do("POST", "/v1/key/rotate", map[string]any{"pubkey": pub})
		fatal(e)
		var out answer
		fatal(json.Unmarshal(data, &out))
		archive := filepath.Join(filepath.Dir(keyPath), "archive", fmt.Sprintf("%s.v%d.key", cli.Agent, out.Version-1))
		fatal(os.MkdirAll(filepath.Dir(archive), 0700))
		fatal(os.WriteFile(archive, old, 0600))
		fatal(os.Rename(next, keyPath))
		fmt.Printf("rotated %s to key version %d (old key archived)\n", cli.Agent, out.Version)
	case "revoke-key":
		fs := flag.NewFlagSet("revoke-key", flag.ExitOnError)
		agent := mustAgentFlag(fs)
		version := fs.Int("version", 0, "old key version to revoke")
		_ = fs.Parse(args)
		_, _, cli, e := openProject("", *agent)
		fatal(e)
		b, e := cli.Do("POST", "/v1/key/revoke", map[string]int{"version": *version})
		fatal(e)
		fmt.Println(string(b))
	case "status":
		fs := flag.NewFlagSet("status", flag.ExitOnError)
		agent := mustAgentFlag(fs)
		_ = fs.Parse(args)
		_, _, cli, e := openProject("", *agent)
		fatal(e)
		b, e := cli.Do("GET", "/v1/leases?session="+url.QueryEscape(cli.Session), nil)
		fatal(e)
		fmt.Println(string(b))
	case "verify":
		fs := flag.NewFlagSet("verify", flag.ExitOnError)
		repo := fs.String("repo", "", "project directory")
		agent := mustAgentFlag(fs)
		_ = fs.Parse(args)
		_, _, cli, e := openProject(*repo, *agent)
		fatal(e)
		var out map[string]any
		data, e := cli.Do("GET", "/v1/verify", nil)
		fatal(e)
		fatal(json.Unmarshal(data, &out))
		pretty, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(pretty))
		if out["ok"] != true {
			os.Exit(1)
		}
	case "render":
		fs := flag.NewFlagSet("render", flag.ExitOnError)
		session := fs.String("session", "", "session override")
		repo := fs.String("repo", "", "project directory")
		agent := mustAgentFlag(fs)
		_ = fs.Parse(args)
		_, cfg, cli, e := openProject(*repo, *agent)
		fatal(e)
		if *session != "" && *session != cfg.Session {
			fatal(errors.New("render can only read the authenticated agent's configured session"))
		}
		data, e := cli.Do("GET", "/v1/doc?session="+url.QueryEscape(cli.Session), nil)
		fatal(e)
		_, e = os.Stdout.Write(data)
		fatal(e)
	case "export":
		fs := flag.NewFlagSet("export", flag.ExitOnError)
		outPath := fs.String("out", "collaborate-ledger.jsonl", "append-only JSONL output path")
		repo := fs.String("repo", "", "project directory")
		adminTokenFile := fs.String("admin-token-file", "", "operator token file; or use COLLAB_ADMIN_TOKEN")
		_ = fs.Parse(args)
		_, cfg, e := repoAndConfig(*repo)
		fatal(e)
		h, e := clientHTTP(cfg)
		fatal(e)
		token, e := adminToken(*adminTokenFile)
		fatal(e)
		headers := map[string]string{}
		if token != "" {
			headers["X-Collab-Admin-Token"] = token
		}
		var data json.RawMessage
		e = httpJSONWithHeaders(h, "GET", strings.TrimRight(cfg.URL, "/")+"/v1/export", nil, &data, headers)
		fatal(e)
		fatal(saveAppendOnlyExport(*outPath, data))
		fmt.Printf("ledger export appended/verified: %s\n", *outPath)
	case "adapter":
		if len(args) == 0 || args[0] == "list" {
			fmt.Println("echo\nopenai-compatible")
			return
		}
		if args[0] != "run" {
			fatal(errors.New("usage: collaborate adapter list | run --name echo|openai-compatible --prompt TEXT"))
		}
		fs := flag.NewFlagSet("adapter run", flag.ExitOnError)
		name := fs.String("name", "echo", "registered adapter name")
		prompt := fs.String("prompt", "", "adapter prompt")
		agent := mustAgentFlag(fs)
		_ = fs.Parse(args[1:])
		_, _, cli, e := openProject("", *agent)
		fatal(e)
		registry := adapter.NewRegistry()
		fatal(registry.Register(adapter.Echo{}))
		if *name == "openai-compatible" {
			endpoint := os.Getenv("COLLAB_MODEL_BASE_URL")
			if endpoint == "" {
				endpoint = "https://api.openai.com/v1"
			}
			model := os.Getenv("COLLAB_MODEL")
			apiKey := os.Getenv("COLLAB_MODEL_API_KEY")
			if apiKey == "" {
				apiKey = os.Getenv("OPENAI_API_KEY")
			}
			provider, err := adapter.NewOpenAICompatible(endpoint, model, apiKey, nil)
			fatal(err)
			fatal(registry.Register(provider))
		}
		a, e := registry.Get(*name)
		fatal(e)
		result, e := a.Run(context.Background(), adapter.Request{Prompt: *prompt}, adapter.NewSignedBroker(cli))
		fatal(e)
		fmt.Println(result.Text)
	case "mcp":
		fs := flag.NewFlagSet("mcp", flag.ExitOnError)
		repo := fs.String("repo", "", "project directory containing .collab configuration")
		agent := mustAgentFlag(fs)
		_ = fs.Parse(args)
		_, _, cli, err := openProject(*repo, *agent)
		fatal(err)
		fatal((&mcpserver.Server{Broker: cli}).Serve(os.Stdin, os.Stdout))
	default:
		usage()
		os.Exit(2)
	}
}

var _ = broker.Server{}
