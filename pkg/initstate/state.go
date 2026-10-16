package initstate

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/naymi/kubeconfig-composer/pkg/cleaner"
)

// State — то, что init помнит между запусками: отпечаток источников, по
// которым собран kc.yaml. Version и Updated нужны только человеку, который
// открыл файл.
type State struct {
	Fingerprint string `json:"fingerprint"`
	Version     string `json:"version"`
	Updated     string `json:"updated"`
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
