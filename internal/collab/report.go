package collab

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func adocEscape(s string) string {
	s = strings.ReplaceAll(s, "|", "&#124;")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "`", "'")
	if len(s) > 500 {
		s = s[:500] + "..."
	}
	return s
}
func (s *Store) Report() (string, error) {
	events, err := s.Events(0)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("= collaborate.AsciiDoc: broker session receipt\n:toc:\n:sectnums:\n\n")
	b.WriteString("[WARNING]\n====\nBroker-attested records are not independent model-identity, test, or Git-commit attestations. Verify referenced commits and test results separately.\n====\n\n")
	head := "(empty)"
	if len(events) > 0 {
		head = events[len(events)-1].Hash
	}
	fmt.Fprintf(&b, "== Session summary\n\n* Recorded events: %d\n* Ledger head SHA-256: `%s`\n* Broker public key (hex): `%x`\n\n", len(events), head, s.PublicKey())
	b.WriteString("== Collaboration timeline\n\n[cols=\"1,2,1,1,2\",options=\"header\"]\n|===\n|Seq |UTC |Agent |Action |Event hash\n")
	for _, e := range events {
		fmt.Fprintf(&b, "|%d |%s |%s |%s |`%s`\n", e.Seq, adocEscape(e.Timestamp), adocEscape(e.Actor), adocEscape(e.Type), e.Hash[:16])
	}
	b.WriteString("|===\n\n== Inter-agent messages\n\n")
	messages := 0
	for _, e := range events {
		if e.Type != "MESSAGE" {
			continue
		}
		var p MessageInput
		if json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		fmt.Fprintf(&b, "* *#%d %s → %s*: %s\n", e.Seq, adocEscape(e.Actor), adocEscape(p.To), adocEscape(p.Body))
		messages++
	}
	if messages == 0 {
		b.WriteString("No messages recorded.\n")
	}
	b.WriteString("\n== Work receipts\n\n")
	count := 0
	for _, e := range events {
		if e.Type != "RECEIPT" {
			continue
		}
		var p ReceiptInput
		if json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		fmt.Fprintf(&b, "=== Receipt #%d\n\n* Agent: %s\n* Commit: `%s`\n* Summary: %s\n* Paths: %s\n* Event hash: `%s`\n\n", e.Seq, adocEscape(p.Agent), p.Commit, adocEscape(p.Summary), adocEscape(strings.Join(p.Paths, ", ")), e.Hash)
		count++
	}
	if count == 0 {
		b.WriteString("No commit receipts recorded.\n\n")
	}
	b.WriteString("== Test gate reports (untrusted until independently reproduced)\n\n")
	gates := 0
	for _, e := range events {
		if e.Type != "GATE_REPORTED" {
			continue
		}
		var p GateInput
		if json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		fmt.Fprintf(&b, "* #%d %s: command `%s`, exit %d, output SHA-256 `%s`\n", e.Seq, adocEscape(p.Agent), adocEscape(strings.Join(p.Command, " ")), p.ExitCode, p.OutputSHA256)
		gates++
	}
	if gates == 0 {
		b.WriteString("No gate runs reported.\n")
	}
	b.WriteString("\n== Reproduction\n\n[source,shell]\n----\ncollab verify -db .collab/state.sqlite -pub .collab/broker.pub -expected-head " + head + "\n# Independently inspect Git commits and replay test gates in a clean checkout.\n----\n")
	return b.String(), nil
}
func WriteReport(s *Store, out string) error {
	data, err := s.Report()
	if err != nil {
		return err
	}
	return os.WriteFile(out, []byte(data), 0644)
}
