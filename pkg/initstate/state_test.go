package initstate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "init-state.json")
	if _, ok := LoadState(path); ok {
		t.Fatal("несуществующий файл не должен читаться как состояние")
	}
	want := State{Fingerprint: "abc", Version: "v1", Updated: "2026-10-09T00:00:00Z"}
	if err := SaveState(path, want); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadState(path)
	if !ok || got != want {
		t.Fatalf("got %+v ok=%v, want %+v", got, ok, want)
	}
}

func TestLoadStateCorruptIsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "init-state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadState(path); ok {
		t.Fatal("повреждённый файл должен считаться отсутствующим состоянием")
	}
}
