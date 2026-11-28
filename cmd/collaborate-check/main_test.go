package main

import (
	"bytes"
	"strings"
	"testing"

	"collaborate/internal/ghcoord"
)

func noEnv(string) string { return "" }

func TestVersion(t *testing.T) {
	for _, arg := range []string{"--version", "version"} {
		var out, errOut bytes.Buffer
		if code := run([]string{arg}, &out, &errOut, noEnv); code != 0 {
			t.Fatalf("%s: exit %d", arg, code)
		}
		if !strings.Contains(out.String(), "collaborate-check dev") || !strings.Contains(out.String(), ghcoord.SchemaVersion) {
			t.Fatalf("%s: output %q", arg, out.String())
		}
	}
}

func TestUnknownFlagIsAnError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--definitely-not-a-flag"}, &out, &errOut, noEnv); code != 2 {
		t.Fatalf("exit %d, want 2 (ERROR); stderr %q", code, errOut.String())
	}
}

func TestMissingConfigurationNeverPasses(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut, noEnv); code == 0 {
		t.Fatalf("no repository/token/PR must not PASS; stdout %q", out.String())
	}
}
