package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"collaborate/internal/store"
)

func TestAppendOnlyExportChecksExactPrefixAndDoesNotDuplicate(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "ledger.jsonl")
	events := []store.Event{
		{Seq: 1, Session: "s1", Agent: "a", Type: "SAY", Payload: json.RawMessage(`{"body":"first"}`), TS: 1, PrevHash: "", RequestDigest: "d1", Signature: []byte{1}, KeyVersion: 1, Hash: "hash1"},
		{Seq: 2, Session: "s1", Agent: "a", Type: "SAY", Payload: json.RawMessage(`{"body":"second"}`), TS: 2, PrevHash: "hash1", RequestDigest: "d2", Signature: []byte{2}, KeyVersion: 1, Hash: "hash2"},
	}
	data, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveAppendOnlyExport(filename, data); err != nil {
		t.Fatal(err)
	}
	if err = saveAppendOnlyExport(filename, data); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(strings.TrimSpace(string(b)), "\n") + 1; got != 2 {
		t.Fatalf("repeated export produced %d lines, want 2", got)
	}
	tampered := strings.Replace(string(b), `"first"`, `"tampered"`, 1)
	if tampered == string(b) {
		t.Fatal("test could not locate event payload")
	}
	if err = os.WriteFile(filename, []byte(tampered), 0600); err != nil {
		t.Fatal(err)
	}
	if err = saveAppendOnlyExport(filename, data); err == nil {
		t.Fatal("export accepted modified event content with unchanged sequence/hash")
	}
}
