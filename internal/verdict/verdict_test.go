package verdict

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteRead(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	in := &Verdict{Alarmed: true, Events: []Event{{At: at, Type: "alarm"}}, Patch: &Patch{Class: "rollback", Format: "kubernetes-yaml"}}
	if err := Write(dir, in); err != nil {
		t.Fatal(err)
	}
	out, err := Read(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := out.FirstAlarm(at.Add(-time.Second)); !ok || !got.Equal(at) {
		t.Errorf("first alarm %v %v", got, ok)
	}
	if _, ok := out.FirstAlarm(at.Add(time.Second)); ok {
		t.Error("an alarm before the cutoff was counted")
	}
}

func TestReadRejects(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"old schema":         {`{"schemaVersion":"boards.verdict/v0"}`, "schemaVersion"},
		"alarmed, no event":  {`{"schemaVersion":"boards.verdict/v1","alarmed":true}`, "events say otherwise"},
		"event, not alarmed": {`{"schemaVersion":"boards.verdict/v1","events":[{"at":"2026-10-23T12:00:00Z","type":"alarm"}]}`, "events say otherwise"},
		"unclassed patch":    {`{"schemaVersion":"boards.verdict/v1","patch":{"format":"kubernetes-yaml"}}`, "without a class"},
		"unknown event":      {`{"schemaVersion":"boards.verdict/v1","events":[{"at":"2026-10-23T12:00:00Z","type":"page"}]}`, "/events/0/type"},
		// Caught by the schema (#13).
		"unknown field":       {`{"schemaVersion":"boards.verdict/v1","firstAlarmAfterSeconds":63}`, "firstAlarmAfterSeconds"},
		"culprit, no kind":    {`{"schemaVersion":"boards.verdict/v1","culprits":[{"name":"payments-v2"}]}`, "/culprits/0"},
		"culprit, no name":    {`{"schemaVersion":"boards.verdict/v1","culprits":[{"kind":"Deployment","name":""}]}`, "/culprits/0/name"},
		"patch, other format": {`{"schemaVersion":"boards.verdict/v1","patch":{"class":"rollback","format":"helm-values"}}`, "/patch/format"},
		"patch, no format":    {`{"schemaVersion":"boards.verdict/v1","patch":{"class":"rollback"}}`, "/patch"},
		"bad timestamp":       {`{"schemaVersion":"boards.verdict/v1","alarmed":true,"events":[{"at":"23 Oct 2026","type":"alarm"}]}`, "/events/0/at"},
		"negative tokens":     {`{"schemaVersion":"boards.verdict/v1","tokens":-1}`, "/tokens"},
		"not JSON":            {`{"schemaVersion":`, "verdict.json"},
	} {
		path := filepath.Join(t.TempDir(), FileName)
		if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Read(path)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		} else if !strings.Contains(err.Error(), path) {
			t.Errorf("%s: the message does not name the file: %v", name, err)
		}
	}
}

// The example in the architecture document is what adapter authors copy, so it must pass Read.
func TestArchitectureVerdictExampleReads(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "architecture.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(doc), "### Verdict\n")
	if !ok {
		t.Fatal("architecture document has no Verdict section")
	}
	_, example, ok := strings.Cut(section, "```json\n")
	if !ok {
		t.Fatal("Verdict section has no fenced JSON example")
	}
	example, _, _ = strings.Cut(example, "\n```")
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(example), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err != nil {
		t.Fatalf("the documented example is not a valid verdict: %v", err)
	}
}
