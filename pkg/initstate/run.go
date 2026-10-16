package initstate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/naymi/kubeconfig-composer/pkg/composer"
)

// Options — параметры Run.
type Options struct {
	Dir         string        // каталог источников (--dir)
	Output      string        // собираемый файл (--output)
	Prefix      string        // префикс имён (--prefix)
	MaxSizeMB   int           // порог суммарного размера источников; <= 0 — без порога
	Refresh     bool          // пересобрать сейчас, порог не учитывается
	Version     string        // версия бинарника, входит в отпечаток
	StateDir    string        // ~/.kubeconfig-composer
	Log         io.Writer     // предупреждения; обычно os.Stderr
	LockTimeout time.Duration // сколько ждать блокировку при первой сборке; 0 — 30 с
}

// Result — итог Run.
type Result struct {
	Output   string // абсолютный путь kc.yaml
	ExportOK bool   // kc.yaml существует, можно печатать export (в том числе при ошибке)
	Rebuilt  bool   // kc.yaml был записан заново
}

// Run выполняет алгоритм init: отпечаток, порог, блокировка, пересборка.
// Подробности и порядок шагов — в спеке docs/superpowers/specs/.
func Run(opts Options) (res Result, err error) {
	logf := func(format string, a ...any) {
		if opts.Log != nil {
			fmt.Fprintf(opts.Log, format+"\n", a...)
		}
	}

	dir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return res, err
	}
	output, err := filepath.Abs(opts.Output)
	if err != nil {
		return res, err
	}
	res.Output = output
	defer func() { res.ExportOK = fileExists(output) }()

	snap, err := Scan(dir, output, opts.Version, opts.Prefix, dir, output)
	if err != nil {
		return res, fmt.Errorf("не удалось обойти %s: %w", dir, err)
	}

	statePath := filepath.Join(opts.StateDir, "init-state.json")
	upToDate := func() bool {
		st, ok := LoadState(statePath)
		return ok && st.Fingerprint == snap.Hash && fileExists(output)
	}
	if !opts.Refresh && upToDate() {
		return res, nil
	}

	if !opts.Refresh && opts.MaxSizeMB > 0 && fileExists(output) && snap.TotalSize > int64(opts.MaxSizeMB)<<20 {
		logf("⚠ kc.yaml устарел: источники весят %.1f МБ при пороге %d МБ; запустите `kubeconfig-composer init --refresh`",
			float64(snap.TotalSize)/(1<<20), opts.MaxSizeMB)
		return res, nil
	}

	lockPath := filepath.Join(opts.StateDir, "init.lock")
	lock, err := TryLock(lockPath)
	if err != nil {
		return res, err
	}
	if lock == nil {
		if fileExists(output) {
			return res, nil // собирает другой терминал; kc.yaml заменяется атомарно
		}
		timeout := opts.LockTimeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		if lock, err = WaitLock(lockPath, timeout); err != nil {
			return res, err
		}
	}
	defer lock.Unlock()

	// Пока ждали блокировку, другой процесс мог собрать то же самое.
	if !opts.Refresh && upToDate() {
		return res, nil
	}

	wrote, err := rebuild(opts, dir, output, logf)
	if err != nil {
		return res, err
	}
	res.Rebuilt = wrote

	st := State{Fingerprint: snap.Hash, Version: opts.Version, Updated: time.Now().UTC().Format(time.RFC3339)}
	if err := SaveState(statePath, st); err != nil {
		return res, err
	}
	return res, nil
}

// rebuild собирает kc.yaml из источников и записывает его, если содержимое
// изменилось. Возвращает true, если файл был записан.
func rebuild(opts Options, dir, output string, logf func(string, ...any)) (bool, error) {
	sess, serr := ReadSession(output)
	if serr != nil {
		logf("⚠ не удалось прочитать прежний %s, состояние сессии не сохранено: %v", output, serr)
		sess = nil
	}

	c := composer.New(true)
	c.SetBaseDir(dir)
	c.SetNamePrefix(opts.Prefix)
	warnings, err := CaptureStdout(func() error {
		if err := c.ScanDirectory(dir, output); err != nil {
			return err
		}
		return c.Merge()
	})
	for _, w := range warnings {
		logf("%s", w)
	}
	if err != nil {
		return false, fmt.Errorf("не удалось собрать конфиг: %w", err)
	}

	// current-context ставим до Dedupe: он не схлопывает контекст из current-context.
	if sess != nil {
		sess.ApplyCurrentContext(c.Result())
	}
	c.Dedupe()
	if sess != nil {
		sess.ApplyNamespaces(c.Result())

		if lost := sess.LostForeign(c.Result(), opts.Prefix); len(lost) > 0 {
			list := formatLost(lost)
			backup, berr := backupFile(output, filepath.Join(opts.StateDir, "backups"))
			if berr != nil {
				logf("⚠ в новом %s нет записей, которых нет среди источников: %s; сохранить прежний файл не удалось: %v", output, list, berr)
			} else {
				logf("⚠ в новом %s нет записей, которых нет среди источников: %s; прежний файл сохранён: %s", output, list, backup)
			}
		}
	}

	if fileExists(output) {
		if same, err := c.OutputUnchanged(output); err == nil && same {
			return false, nil
		}
	}
	if err := c.WriteOutputAtomic(output); err != nil {
		return false, err
	}
	return true, nil
}

// maxLostListed — сколько имён пропавших чужих записей попадает в предупреждение.
const maxLostListed = 10

// formatLost форматирует первые maxLostListed имён; про остальные пишет «и ещё N».
func formatLost(lost []string) string {
	shown := lost[:min(maxLostListed, len(lost))]
	out := fmt.Sprintf("%v", shown)
	if rest := len(lost) - len(shown); rest > 0 {
		out += fmt.Sprintf(" и ещё %d", rest)
	}
	return out
}

// backupFile копирует src в backupDir под именем, которое понимает revert:
// "<имя>.backup.<timestamp>".
func backupFile(src, backupDir string) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(backupDir, fmt.Sprintf("%s.backup.%s", filepath.Base(src), time.Now().Format("20060102-150405")))
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return "", err
	}
	return dst, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
