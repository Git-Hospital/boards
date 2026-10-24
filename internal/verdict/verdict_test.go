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
	in := &Verdict{Alarmed: true, Events: []Event{{At: at, Type: "alarm"}}, Patch: &Patch{Class: "rollback"}}
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
		"unknown event":      {`{"schemaVersion":"boards.verdict/v1","events":[{"at":"2026-10-23T12:00:00Z","type":"page"}]}`, "event type"},
	} {
		path := filepath.Join(t.TempDir(), FileName)
		if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Read(path)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
}
