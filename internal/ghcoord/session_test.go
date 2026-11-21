package ghcoord

import (
	"strings"
	"testing"
)

func TestSessionMembersJoinLeaveAndRejoin(t *testing.T) {
	reg := mustRegistry(t, testRegistry)
	comments := []ghComment{
		{ID: 14, Body: "Collaborate-Join: Gemini CLI", User: ghUser{Login: "gemini-code-assist[bot]"}},
		{ID: 10, Body: "Review only", User: ghUser{Login: "DeontewattsV1"}},
		{ID: 11, Body: "Collaborate-Join: Claude Code", User: ghUser{Login: "claude[bot]"}},
		{ID: 12, Body: "Collaborate-Leave", User: ghUser{Login: "claude[bot]"}},
		{ID: 13, Body: "Collaborate-Join: Claude Code", User: ghUser{Login: "claude[bot]"}},
		{ID: 15, Body: "Collaborate-Join: Pretend User", User: ghUser{Login: "github-actions[bot]"}},
	}
	got := ParseSessionMembers(reg, comments)
	if len(got) != 2 {
		t.Fatalf("active members = %+v, want 2", got)
	}
	if got[0].Login != "claude[bot]" || got[0].Label != "Claude Code" || got[0].CommentID != 13 {
		t.Fatalf("Claude rejoin lost: %+v", got)
	}
	if got[1].Login != "gemini-code-assist[bot]" {
		t.Fatalf("Gemini membership lost: %+v", got)
	}
	if got[0].Attribution.Source != "login" {
		t.Fatalf("GitHub login attribution not preserved: %+v", got[0].Attribution)
	}
}

func TestSessionMembersRejectMalformedAndSpoofing(t *testing.T) {
	reg := mustRegistry(t, testRegistry)
	comments := []ghComment{
		{ID: 1, Body: "Collaborate-Join: <script>alert(1)</script>", User: ghUser{Login: "attacker"}},
		{ID: 2, Body: "Collaborate-Join: Claude\nCollaborate-Leave", User: ghUser{Login: "attacker"}},
		{ID: 3, Body: "Collaborate-Join: GPT\nCollaborate-Join: another label `bad`", User: ghUser{Login: "attacker"}},
		{ID: 4, Body: CommentMarker + "\nCollaborate-Join: trusted", User: ghUser{Login: "attacker"}},
		{ID: 5, Body: "> Collaborate-Join: Forged", User: ghUser{Login: "mallory"}},
	}
	got := ParseSessionMembers(reg, comments)
	if len(got) != 1 || got[0].Login != "attacker" || got[0].Label != "GPT" {
		t.Fatalf("untrusted join parsing: %+v", got)
	}
	if got[0].Attribution.Verified != true || got[0].Attribution.AgentID == "claude" {
		t.Fatalf("self-reported label must not confer Claude identity: %+v", got[0].Attribution)
	}
	if validSessionLabel(strings.Repeat("x", 65)) || validSessionLabel(" a") || validSessionLabel("Claude\tCode") {
		t.Fatal("unsafe session label accepted")
	}
	if !validSessionLabel("Claude Code") || !validSessionLabel("GPT-6/Codex") {
		t.Fatal("valid session label rejected")
	}
}
