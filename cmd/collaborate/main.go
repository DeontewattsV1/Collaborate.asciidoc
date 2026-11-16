package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"collaborate/internal/broker"
	"collaborate/internal/client"
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
  serve --db FILE [--addr 127.0.0.1:8787] [--lease-ttl 90s]
  init --session ID [--repo DIR] [--url URL]
  register --agent ID --model MODEL [--repo DIR]
  claim --intent TEXT PATH...
  release PATH...
  renew
  say MESSAGE...
  commit -m MESSAGE       commit staged changes and record a signed receipt
  receipt                 record a receipt for current HEAD (without committing)
  spawn --agent ID --branch BRANCH
  status
  verify
  render [--session ID]

Set COLLAB_AGENT to select the registered agent for signed commands.
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
func httpJSON(method, url string, body any, out any) error {
	b, e := json.Marshal(body)
	if e != nil {
		return e
	}
	req, e := http.NewRequest(method, url, strings.NewReader(string(b)))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
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
	return cfg, client.New(cfg.URL, cfg.Session, agent, key), nil
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
func repoAndConfig(explicit string) (string, client.Config, error) {
	r, e := rootOrCwd(explicit)
	if e != nil {
		return "", client.Config{}, e
	}
	c, e := config(r)
	return r, c, e
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
		addr := fs.String("addr", "127.0.0.1:8787", "loopback listen address")
		ttl := fs.Duration("lease-ttl", 90*time.Second, "lease lifetime")
		_ = fs.Parse(args)
		if dir := filepath.Dir(*db); dir != "." {
			fatal(os.MkdirAll(dir, 0700))
		}
		s, e := store.Open(*db)
		fatal(e)
		defer s.Close()
		fmt.Printf("collaborate broker listening on %s (lease TTL %s)\n", *addr, ttl.String())
		fatal((&broker.Server{Store: s, LeaseTTL: *ttl}).Listen(*addr))
	case "init":
		fs := flag.NewFlagSet("init", flag.ExitOnError)
		session := fs.String("session", "", "session id")
		repo := fs.String("repo", ".", "Git repository directory")
		url := fs.String("url", defaultURL, "broker URL")
		_ = fs.Parse(args)
		if *session == "" {
			fatal(errors.New("--session is required"))
		}
		r := abs(*repo)
		if _, e := os.Stat(filepath.Join(r, ".git")); e != nil {
			fatal(fmt.Errorf("%s is not a Git worktree", r))
		}
		cfg := client.Config{URL: *url, Session: *session, Repo: r}
		fatal(client.SaveConfig(r, cfg))
		e := httpJSON("POST", strings.TrimRight(cfg.URL, "/")+"/v1/sessions", map[string]string{"id": cfg.Session, "repo": cfg.Repo}, nil)
		fatal(e)
		fmt.Printf("initialized session %s in %s\n", cfg.Session, r)
	case "register":
		fs := flag.NewFlagSet("register", flag.ExitOnError)
		agent := fs.String("agent", "", "agent ID")
		model := fs.String("model", "", "model name/version")
		repo := fs.String("repo", "", "project directory")
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
		var out map[string]any
		e = httpJSON("POST", strings.TrimRight(cfg.URL, "/")+"/v1/agents?session="+cfg.Session, map[string]any{"agent": *agent, "model": *model, "pubkey": pub}, &out)
		fatal(e)
		fatal(client.SaveKey(keyPath, priv))
		fmt.Printf("registered %s (%s); private key saved locally\n", *agent, *model)
	case "claim", "release", "renew", "say", "commit", "receipt", "spawn", "status", "verify", "render":
		switch os.Args[1] {
		case "claim":
			fs := flag.NewFlagSet("claim", flag.ExitOnError)
			intent := fs.String("intent", "", "why paths are needed")
			agent := fs.String("agent", "", "agent ID override")
			_ = fs.Parse(args)
			r, c, cli, e := openProject("", *agent)
			fatal(e)
			_ = c
			paths := fs.Args()
			if *intent == "" {
				fatal(errors.New("--intent is required"))
			}
			b, e := cli.Do("POST", "/v1/claim", map[string]any{"intent": *intent, "paths": paths})
			fatal(e)
			fmt.Println(string(b))
			_ = r
		case "release":
			fs := flag.NewFlagSet("release", flag.ExitOnError)
			agent := fs.String("agent", "", "agent ID override")
			_ = fs.Parse(args)
			_, _, cli, e := openProject("", *agent)
			fatal(e)
			b, e := cli.Do("POST", "/v1/release", map[string]any{"paths": fs.Args()})
			fatal(e)
			fmt.Println(string(b))
		case "renew":
			fs := flag.NewFlagSet("renew", flag.ExitOnError)
			agent := fs.String("agent", "", "agent ID override")
			_ = fs.Parse(args)
			_, _, cli, e := openProject("", *agent)
			fatal(e)
			b, e := cli.Do("POST", "/v1/renew", map[string]any{})
			fatal(e)
			fmt.Println(string(b))
		case "say":
			fs := flag.NewFlagSet("say", flag.ExitOnError)
			agent := fs.String("agent", "", "agent ID override")
			_ = fs.Parse(args)
			_, _, cli, e := openProject("", *agent)
			fatal(e)
			if len(fs.Args()) == 0 {
				fatal(errors.New("message required"))
			}
			b, e := cli.Do("POST", "/v1/say", map[string]string{"body": strings.Join(fs.Args(), " ")})
			fatal(e)
			fmt.Println(string(b))
		case "commit", "receipt":
			fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
			message := fs.String("m", "", "commit message")
			agent := fs.String("agent", "", "agent ID override")
			_ = fs.Parse(args)
			r, _, cli, e := openProject("", *agent)
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
			patch, e := runGit(wd, "show", "--format=", "--no-ext-diff", sha)
			fatal(e)
			sum := sha256.Sum256(patch)
			filesText, e := gitOut(wd, "diff-tree", "--no-commit-id", "--name-only", "-r", sha)
			fatal(e)
			files := []string{}
			for _, f := range strings.Split(filesText, "\n") {
				if f != "" {
					files = append(files, f)
				}
			}
			sort.Strings(files)
			b, e := cli.Do("POST", "/v1/receipt", map[string]any{"commit": sha, "diff_sha": hex.EncodeToString(sum[:]), "files": files})
			fatal(e)
			fmt.Printf("receipt stored for %s: %s\n", sha[:12], strings.TrimSpace(string(b)))
			_ = r
		case "spawn":
			fs := flag.NewFlagSet("spawn", flag.ExitOnError)
			agent := fs.String("agent", "", "agent ID")
			branch := fs.String("branch", "", "new branch")
			_ = fs.Parse(args)
			if *agent == "" || *branch == "" {
				fatal(errors.New("--agent and --branch are required"))
			}
			r, _, e := repoAndConfig("")
			fatal(e)
			wt := filepath.Join(r, ".collab", "wt", *agent)
			out, err := runGit(r, "worktree", "add", "-b", *branch, wt, "HEAD")
			if err != nil {
				fatal(fmt.Errorf("git worktree add: %s", strings.TrimSpace(string(out))))
			}
			fmt.Printf("created %s\n", wt)
		case "status":
			fs := flag.NewFlagSet("status", flag.ExitOnError)
			agent := fs.String("agent", "", "agent ID override")
			_ = fs.Parse(args)
			r, _, cli, e := openProject("", *agent)
			fatal(e)
			b, e := cli.Do("GET", "/v1/leases?session="+cli.Session, nil)
			fatal(e)
			fmt.Println(string(b))
			_ = r
		case "verify":
			fs := flag.NewFlagSet("verify", flag.ExitOnError)
			repo := fs.String("repo", "", "project directory")
			_ = fs.Parse(args)
			_, cfg, e := repoAndConfig(*repo)
			fatal(e)
			var out map[string]any
			e = httpJSON("GET", strings.TrimRight(cfg.URL, "/")+"/v1/verify", nil, &out)
			fatal(e)
			pretty, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(pretty))
			if out["ok"] != true {
				os.Exit(1)
			}
		case "render":
			fs := flag.NewFlagSet("render", flag.ExitOnError)
			session := fs.String("session", "", "session override")
			repo := fs.String("repo", "", "project directory")
			_ = fs.Parse(args)
			_, cfg, e := repoAndConfig(*repo)
			fatal(e)
			if *session != "" {
				cfg.Session = *session
			}
			resp, e := http.Get(strings.TrimRight(cfg.URL, "/") + "/v1/doc?session=" + cfg.Session)
			fatal(e)
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				fatal(fmt.Errorf("broker returned %s", resp.Status))
			}
			_, e = io.Copy(os.Stdout, resp.Body)
			fatal(e)
		}
	default:
		usage()
		os.Exit(2)
	}
}
func openProject(explicit, agent string) (string, client.Config, *client.Client, error) {
	r, cfg, e := repoAndConfig(explicit)
	if e != nil {
		return "", cfg, nil, e
	}
	_, cli, e := getClient(r, agent)
	return r, cfg, cli, e
}
