// Command guild is the Dev Guild bridge. It serves the iMessage API on
// loopback, records every line in the agentterm signed log, and can be read
// from Secure ShellFish with `guild tail` or `guild chat`.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"collaborate/internal/guild"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "guild:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: guild <serve|characters|tail|chat|pubkey>")
	}
	switch args[0] {
	case "serve":
		return serve(args[1:])
	case "characters":
		return characters(args[1:])
	case "tail":
		return tail(args[1:])
	case "chat":
		return chat(args[1:])
	case "pubkey":
		return pubkey(args[1:])
	case "-h", "--help", "help":
		fmt.Println("usage: guild <serve|characters|tail|chat|pubkey>")
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func dataDir(fs *flag.FlagSet) *string {
	dir := ".agentterm"
	if v := os.Getenv("GUILD_DATA"); v != "" {
		dir = v
	}
	return fs.String("data", dir, "directory for guild.jsonl and guild.key")
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	dir := dataDir(fs)
	addr := fs.String("addr", "127.0.0.1:8790", "loopback listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	token := os.Getenv("GUILD_OWNER_TOKEN")
	if token == "" {
		return errors.New("GUILD_OWNER_TOKEN is required (it never goes in the repo or the browser)")
	}
	demo := os.Getenv("GUILD_DEMO") == "1"
	svc, err := guild.Open(*dir, demo)
	if err != nil {
		return err
	}
	defer svc.Term.Close()
	fmt.Printf("dev guild log %s/guild.jsonl\n", *dir)
	fmt.Printf("verify with: agentterm verify --file %s/guild.jsonl --server-key %s\n", *dir, svc.PublicKey())
	printRoster(os.Stdout, svc)
	if demo {
		fmt.Println("demo voice is on: replies stay in character and no provider is called")
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           guild.Handler(svc, token, os.Getenv("GUILD_WEB_ORIGIN")),
		ReadHeaderTimeout: 5 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	fmt.Printf("listening on http://%s\n", *addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func characters(args []string) error {
	fs := flag.NewFlagSet("characters", flag.ContinueOnError)
	_ = dataDir(fs)
	demo := fs.Bool("demo", os.Getenv("GUILD_DEMO") == "1", "show the demo roster")
	if err := fs.Parse(args); err != nil {
		return err
	}
	svc := &guild.Service{Reg: guild.Load(), Demo: *demo}
	printRoster(os.Stdout, svc)
	return nil
}

func printRoster(w io.Writer, svc *guild.Service) {
	fmt.Fprintln(w, "Dev Guild")
	for _, c := range svc.Characters() {
		state := "offline"
		if c.Online {
			state = "online "
		}
		fmt.Fprintf(w, "  %-8s  %-16s  %s\n", state, c.Name, c.Note)
	}
}

func pubkey(args []string) error {
	fs := flag.NewFlagSet("pubkey", flag.ContinueOnError)
	dir := dataDir(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	svc, err := guild.Open(*dir, false)
	if err != nil {
		return err
	}
	defer svc.Term.Close()
	fmt.Println(svc.PublicKey())
	return nil
}

func tail(args []string) error {
	fs := flag.NewFlagSet("tail", flag.ContinueOnError)
	dir := dataDir(fs)
	thread := fs.String("thread", "", "only this thread id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	svc, err := guild.Open(*dir, false)
	if err != nil {
		return err
	}
	defer svc.Term.Close()
	if _, err := svc.Verify(); err != nil {
		return err
	}
	for _, t := range svc.Threads() {
		if *thread != "" && t.ID != *thread {
			continue
		}
		fmt.Printf("== %s (%s)\n", t.Title, t.ID)
		msgs, err := svc.Messages(t.ID, 0)
		if err != nil {
			return err
		}
		for _, m := range msgs {
			fmt.Printf("%s  %-12s  %s\n", m.Time, m.Name, m.Text)
		}
	}
	return nil
}

func chat(args []string) error {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	addr := fs.String("addr", "http://127.0.0.1:8790", "guild serve address")
	thread := fs.String("thread", "guild", "thread id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	token := os.Getenv("GUILD_OWNER_TOKEN")
	if token == "" {
		return errors.New("GUILD_OWNER_TOKEN is required")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	printThread(client, *addr, token, *thread, 0)
	in := bufio.NewScanner(os.Stdin)
	fmt.Printf("chat %s (type a line, @mention a character, Ctrl-D to leave)\n", *thread)
	var after int64
	for in.Scan() {
		text := strings.TrimSpace(in.Text())
		if text == "" {
			continue
		}
		body, _ := json.Marshal(map[string]string{"text": text})
		req, err := http.NewRequest(http.MethodPost, strings.TrimRight(*addr, "/")+"/v1/threads/"+*thread+"/messages", strings.NewReader(string(body)))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			return err
		}
		res.Body.Close()
		if res.StatusCode != http.StatusAccepted && res.StatusCode != http.StatusOK {
			return fmt.Errorf("post returned %s", res.Status)
		}
		time.Sleep(300 * time.Millisecond)
		after = printThread(client, *addr, token, *thread, after)
	}
	return in.Err()
}

func printThread(client *http.Client, addr, token, thread string, after int64) int64 {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/threads/%s/messages?after=%d", strings.TrimRight(addr, "/"), thread, after), nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return after
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return after
	}
	defer res.Body.Close()
	var payload struct {
		Messages []struct {
			Seq  int64  `json:"seq"`
			Name string `json:"name"`
			Text string `json:"text"`
			Time string `json:"time"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return after
	}
	for _, m := range payload.Messages {
		fmt.Printf("%s  %-12s  %s\n", m.Time, m.Name, m.Text)
		if m.Seq > after {
			after = m.Seq
		}
	}
	return after
}
