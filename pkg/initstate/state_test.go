package initstate

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "init-state-abc.json")
	if _, ok := LoadState(path); ok {
		t.Fatal("несуществующий файл не должен читаться как состояние")
	}
	want := State{
		Fingerprint:      "abc",
		Version:          "v1",
		Updated:          "2026-10-09T00:00:00Z",
		Names:            []string{"kc:a-c:a.yaml", "kc:a:a.yaml"},
		SourceNamespaces: map[string]string{"kc:a:a.yaml": "team-a"},
	}
	if err := SaveState(path, want); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadState(path)
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v ok=%v, want %+v", got, ok, want)
	}
}

func TestLoadStateCorruptIsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "init-state-abc.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadState(path); ok {
		t.Fatal("повреждённый файл должен считаться отсутствующим состоянием")
	}
}

func TestPairKeyDependsOnBothPaths(t *testing.T) {
	a := pairKey("/k1", "/k1/kc.yaml")
	if len(a) != 12 {
		t.Fatalf("ключ %q: ожидалось 12 hex-символов", a)
	}
	if a != pairKey("/k1", "/k1/kc.yaml") {
		t.Error("ключ должен быть детерминированным")
	}
	for _, other := range []string{
		pairKey("/k2", "/k1/kc.yaml"),
		pairKey("/k1", "/k1/other.yaml"),
		pairKey("/k1/kc.yaml", "/k1"),
	} {
		if other == a {
			t.Errorf("разные пары дали один ключ %q", a)
		}
	}
}
