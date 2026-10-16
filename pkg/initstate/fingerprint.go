package initstate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/naymi/kubeconfig-composer/pkg/scanner"
)

// Snapshot — результат обхода каталога источников: отпечаток для сравнения с
// прошлой сборкой и суммарный размер для проверки порога.
type Snapshot struct {
	Hash      string
	TotalSize int64
	Files     int
}

// Scan обходит dir через stat, не читая содержимое файлов. Пропускаются
// скрытые файлы (в том числе временные .kc.yaml.tmp-*), служебные файлы из
// scanner.IsServiceFile (kubectx, *.lock), сам output и каталоги из
// scanner.IsSkippedDir (в том числе kubens/); корень обхода не проверяется —
// ~/.kube скрытый.
// Символические ссылки на файлы учитываются по цели (os.Stat). salt
// подмешивается в хеш: версия бинарника и параметры сборки, чтобы их смена
// тоже вызывала пересборку.
func Scan(dir, output string, salt ...string) (*Snapshot, error) {
	absOutput, err := filepath.Abs(output)
	if err != nil {
		return nil, err
	}

	var lines []string
	snap := &Snapshot{}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == dir {
				return err
			}
			return nil // файл или подкаталог исчез или недоступен — пропускаем
		}
		if d.IsDir() {
			if path != dir && scanner.IsSkippedDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || scanner.IsServiceFile(d.Name()) {
			return nil
		}
		if abs, aerr := filepath.Abs(path); aerr == nil && abs == absOutput {
			return nil
		}
		info, serr := os.Stat(path)
		if serr != nil || !info.Mode().IsRegular() {
			return nil
		}
		lines = append(lines, fmt.Sprintf("%s\t%d\t%d", path, info.ModTime().UnixNano(), info.Size()))
		snap.TotalSize += info.Size()
		snap.Files++
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(lines)
	h := sha256.New()
	for _, s := range salt {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	h.Write([]byte(strings.Join(lines, "\n")))
	snap.Hash = hex.EncodeToString(h.Sum(nil))
	return snap, nil
}
