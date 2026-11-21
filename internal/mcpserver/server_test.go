package mcpserver

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"collaborate/internal/broker"
	"collaborate/internal/client"
	"collaborate/internal/store"
)

func runRPC(t *testing.T, srv *Server, requests ...string) []map[string]any {
	t.Helper()
	var out strings.Builder
	if err := srv.Serve(strings.NewReader(strings.Join(requests, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(strings.NewReader(out.String()))
	var results []map[string]any
	for sc.Scan() {
		var v map[string]any
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			t.Fatalf("invalid JSON-RPC response %q: %v", sc.Text(), err)
		}
		results = append(results, v)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return results
}
func rpc(method string, id int, params any) string {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return string(b)
}

func TestMCPToolsAreLimitedToCoordinationAndProtocolHandshake(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cli := client.New("http://127.0.0.1:1", "s", "a", priv)
	srv := &Server{Broker: cli}
	got := runRPC(t, srv,
		rpc("initialize", 1, map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "test", "version": "1"}}),
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		rpc("tools/list", 2, map[string]any{}),
		rpc("tools/call", 3, map[string]any{"name": "collaborate_shell", "arguments": map[string]any{}}),
	)
	if len(got) != 3 {
		t.Fatalf("responses=%d, want 3 because notifications have no response", len(got))
	}
	init := got[0]["result"].(map[string]any)
	if init["protocolVersion"] != protocolVersion {
		t.Fatalf("version=%v", init["protocolVersion"])
	}
	list := got[1]["result"].(map[string]any)
	tools := list["tools"].([]any)
	if len(tools) != 6 {
		t.Fatalf("listed %d tools, want six", len(tools))
	}
	for _, v := range tools {
		name := v.(map[string]any)["name"].(string)
		if strings.Contains(name, "shell") || strings.Contains(name, "write") || strings.Contains(name, "exec") {
			t.Fatalf("unsafe tool exposed: %s", name)
		}
	}
	call := got[2]["result"].(map[string]any)
	if call["isError"] != true {
		t.Fatalf("unknown tool was not marked error: %+v", call)
	}
	_ = pub
}

func TestTwoAgentMCPClientsShareBrokerWithSeparateSignatures(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "mcp.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.CreateSession("mcp-session", "/tmp/mcp-test-repo"); err != nil {
		t.Fatal(err)
	}
	keys := map[string]ed25519.PrivateKey{}
	for _, agent := range []string{"agent-a", "agent-b"} {
		pub, priv, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		keys[agent] = priv
		if e = s.Register("mcp-session", agent, "synthetic-test-client", pub); e != nil {
			t.Fatal(e)
		}
	}
	ts := httptest.NewServer((&broker.Server{Store: s}).Handler())
	defer ts.Close()
	makeServer := func(agent string) *Server {
		return &Server{Broker: client.New(ts.URL, "mcp-session", agent, keys[agent])}
	}
	for i, agent := range []string{"agent-a", "agent-b"} {
		msg := fmt.Sprintf("hello from %s", agent)
		r := runRPC(t, makeServer(agent), rpc("tools/call", 1, map[string]any{"name": "collaborate_say", "arguments": map[string]any{"message": msg}}))
		if len(r) != 1 || r[0]["error"] != nil {
			t.Fatalf("%s MCP call failed: %+v", agent, r)
		}
		_ = i
	}
	r := runRPC(t, makeServer("agent-b"), rpc("tools/call", 2, map[string]any{"name": "collaborate_inbox", "arguments": map[string]any{"after": 0}}))
	content := r[0]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	var messages []map[string]any
	if err = json.Unmarshal([]byte(content), &messages); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0]["agent"] != "agent-a" || messages[1]["agent"] != "agent-b" {
		t.Fatalf("shared MCP inbox=%s", content)
	}
}
