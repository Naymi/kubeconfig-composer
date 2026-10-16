package initstate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/naymi/kubeconfig-composer/pkg/cleaner"
)

// State — то, что init помнит между запусками для одной пары (--dir,
// --output). Version и Updated нужны только человеку, который открыл файл.
type State struct {
	// Fingerprint — отпечаток источников, по которым собран output.
	Fingerprint string `json:"fingerprint"`
	Version     string `json:"version"`
	Updated     string `json:"updated"`
	// Names — отсортированные имена кластеров, пользователей и контекстов,
	// которые init записал или подтвердил на прошлой сборке. Всё остальное в
	// output — чужое: при пропаже таких записей делается бэкап (LostForeign).
	Names []string `json:"names,omitempty"`
	// SourceNamespaces — namespace каждого контекста в сборке из источников
	// (до переноса namespace из прежнего output). По нему ApplyNamespaces
	// отличает правку пользователя в output от правки в источнике.
	SourceNamespaces map[string]string `json:"source_namespaces,omitempty"`
}

// owned возвращает State.Names множеством для LostForeign.
func (s State) owned() map[string]bool {
	m := make(map[string]bool, len(s.Names))
	for _, n := range s.Names {
		m[n] = true
	}
	return m
}

// pairKey — ключ пары (dir, output) для имён файлов состояния и блокировки:
// первые 12 hex sha256. Пути должны быть уже развёрнуты (resolvePaths), иначе
// одна пара, записанная через ссылку и напрямую, получит два ключа.
func pairKey(dir, output string) string {
	sum := sha256.Sum256([]byte(dir + "\x00" + output))
	return hex.EncodeToString(sum[:])[:12]
}

// statePaths возвращает файл состояния и файл блокировки init для пары:
// <stateDir>/init-state-<key>.json и <stateDir>/init-<key>.lock.
func statePaths(stateDir, dir, output string) (statePath, lockPath string) {
	key := pairKey(dir, output)
	return filepath.Join(stateDir, "init-state-"+key+".json"),
		filepath.Join(stateDir, "init-"+key+".lock")
}

// LoadState читает состояние. Отсутствующий или повреждённый файл возвращает
// false: init тогда просто пересоберёт kc.yaml.
func LoadState(path string) (State, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, false
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil || s.Fingerprint == "" {
		return State{}, false
	}
	return s, true
}

// SaveState атомарно записывает состояние, создавая каталог при необходимости.
func SaveState(path string, s State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("не удалось сериализовать состояние: %w", err)
	}
	return cleaner.WriteFileAtomic(path, data, 0o600)
}
