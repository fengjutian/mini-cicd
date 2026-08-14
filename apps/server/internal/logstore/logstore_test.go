package logstore

import (
	"os"
	"strings"
	"testing"
)

func TestRedactsBeforeDiskAndPublish(t *testing.T) {
	store, err := New(t.TempDir(), 4096)
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Open("project", 1, []string{"very-secret"})
	if err != nil {
		t.Fatal(err)
	}
	ch, unsub := store.Subscribe(1)
	defer unsub()
	if err = w.WriteStep(2, "output", "token=very-secret"); err != nil {
		t.Fatal(err)
	}
	e := <-ch
	if strings.Contains(e.Message, "very-secret") || !strings.Contains(e.Message, "********") {
		t.Fatalf("published message was not redacted: %q", e.Message)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(store.Path("project", 1))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "very-secret") {
		t.Fatal("secret was written to disk")
	}
	entries, err := store.ReadAfter("project", 1, 0)
	if err != nil || len(entries) != 1 || entries[0].Sequence != 1 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
}

func TestLogLimitWritesSingleTruncationMarker(t *testing.T) {
	store, err := New(t.TempDir(), 180)
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Open("p", 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err = w.WriteStep(1, "output", strings.Repeat("x", 100)); err != nil {
			t.Fatal(err)
		}
	}
	_ = w.Close()
	entries, err := store.ReadAfter("p", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	markers := 0
	for _, e := range entries {
		if strings.Contains(e.Message, "log truncated") {
			markers++
		}
	}
	if markers != 1 {
		t.Fatalf("expected one marker, got %d: %#v", markers, entries)
	}
}
