package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunWithoutCommandShowsUsage(t *testing.T) {
	err := run(nil, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "usage: agentterm") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	if err := run([]string{"no-such-command"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("unknown command must fail")
	}
}

func TestDefaultSessionPathHonoursEnv(t *testing.T) {
	t.Setenv("AGENTTERM_SESSION", "/tmp/x/session.json")
	if got := defaultSessionPath(); got != "/tmp/x/session.json" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("AGENTTERM_SESSION", "")
	if got := defaultSessionPath(); !strings.HasSuffix(got, ".agentterm/session.json") {
		t.Fatalf("got %q", got)
	}
}

func TestMultiFlagAndFirstNonEmpty(t *testing.T) {
	var m multi
	_ = m.Set("a")
	_ = m.Set("b")
	if m.String() != "a,b" {
		t.Fatalf("multi = %q", m.String())
	}
	if firstNonEmpty("", "", "x", "y") != "x" || firstNonEmpty() != "" {
		t.Fatal("firstNonEmpty")
	}
}
