package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPHandshakeAndToolList(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := serveMCP(strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 replies, got %d: %s", len(lines), out.String())
	}
	var reply map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &reply); err != nil {
		t.Fatal(err)
	}
	res := reply["result"].(map[string]any)
	if res["protocolVersion"] != "2024-11-05" {
		t.Fatalf("bad handshake: %v", res)
	}
	if !strings.Contains(lines[1], "collab_say") || !strings.Contains(lines[1], "collab_claim") {
		t.Fatal("tool list incomplete")
	}
}
func TestMCPUnknownTool(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nonexistent","arguments":{}}}` + "\n"
	var out bytes.Buffer
	if err := serveMCP(strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "unknown collaboration tool") {
		t.Fatal(out.String())
	}
}

func TestMCPUnsupportedVersionDoesNotEcho(t *testing.T) {
	var out bytes.Buffer
	if err := serveMCP(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"future-9999"}}`+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "future-9999") || !strings.Contains(out.String(), "2024-11-05") {
		t.Fatal("unsupported version falsely accepted: " + out.String())
	}
}
