package render

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"collaborate/internal/store"
)

// AsciiDoc renders a fixed-order report; it makes no wall-clock reads, so the same ledger yields the same bytes.
func AsciiDoc(session string, events []store.Event) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "= collaborate: session %s\n:session: %s\n:ledger-events: %d\n", escape(session), escape(session), len(events))
	head := "genesis"
	if len(events) > 0 {
		head = events[len(events)-1].Hash
	}
	fmt.Fprintf(&b, ":ledger-head: %s\n\n", head)
	b.WriteString("== Activity\n\n[cols=\"1,2,2,4\",options=\"header\"]\n|===\n|Seq |Time (UTC) |Agent |Event\n")
	for _, e := range events {
		fmt.Fprintf(&b, "|%d |%s |%s |%s\n", e.Seq, escape(timeText(e.TS)), escape(e.Agent), escape(eventText(e)))
	}
	b.WriteString("|===\n\n== Receipts\n\n[cols=\"1,1,5\",options=\"header\"]\n|===\n|Seq |Agent |Receipt details\n")
	n := 0
	for _, e := range events {
		if e.Type != "RECEIPT" {
			continue
		}
		n++
		var p struct {
			Commit  string   `json:"commit"`
			DiffSHA string   `json:"diff_sha"`
			Files   []string `json:"files"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		details := "commit `" + escape(p.Commit) + "`; files: "
		if len(p.Files) == 0 {
			details += "(not recorded)"
		} else {
			for i, f := range p.Files {
				if i > 0 {
					details += ", "
				}
				details += "`" + escape(f) + "`"
			}
		}
		details += "; diff sha256: `" + escape(p.DiffSHA) + "`"
		fmt.Fprintf(&b, "|%d |%s |%s\n", e.Seq, escape(e.Agent), details)
	}
	if n == 0 {
		b.WriteString("|— |— |No commit receipts recorded\n")
	}
	b.WriteString("|===\n\n== Integrity\n\nVerify the event chain and signatures with `collaborate verify`.\n")
	return []byte(b.String())
}
func eventText(e store.Event) string {
	var p map[string]any
	_ = json.Unmarshal(e.Payload, &p)
	switch e.Type {
	case "CLAIM":
		return fmt.Sprintf("Claimed %v — %v", p["paths"], p["intent"])
	case "RELEASE":
		return fmt.Sprintf("Released %v", p["paths"])
	case "SAY":
		return fmt.Sprint(p["body"])
	case "RECEIPT":
		return fmt.Sprintf("Commit receipt %v (diff %v)", p["commit"], p["diff_sha"])
	default:
		return e.Type
	}
}
func timeText(ms int64) string {
	return fmt.Sprintf("%s", time.UnixMilli(ms).UTC().Format("2006-01-02 15:04:05"))
}
func escape(s string) string { return strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ").Replace(s) }
