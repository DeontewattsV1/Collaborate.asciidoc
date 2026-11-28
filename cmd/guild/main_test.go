package main

import (
	"bytes"
	"strings"
	"testing"

	"collaborate/internal/guild"
)

func TestRunCommands(t *testing.T) {
	if err := run(nil); err == nil || !strings.Contains(err.Error(), "usage: guild") {
		t.Fatalf("no args: %v", err)
	}
	if err := run([]string{"bogus"}); err == nil {
		t.Fatal("unknown command must fail")
	}
	if err := run([]string{"help"}); err != nil {
		t.Fatalf("help: %v", err)
	}
}

func TestPrintRoster(t *testing.T) {
	var buf bytes.Buffer
	printRoster(&buf, &guild.Service{Reg: guild.Load(), Demo: true})
	out := buf.String()
	if !strings.HasPrefix(out, "Dev Guild\n") {
		t.Fatalf("roster header missing: %q", out)
	}
	if strings.Count(out, "\n") < 2 {
		t.Fatalf("roster lists no characters: %q", out)
	}
}

func TestPubkeyInTempDir(t *testing.T) {
	if err := pubkey([]string{"--data", t.TempDir()}); err != nil {
		t.Fatalf("pubkey: %v", err)
	}
}
