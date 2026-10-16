package initstate

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/naymi/kubeconfig-composer/pkg/composer"
)

// Options — параметры Run.
type Options struct {
	Dir         string        // каталог источников (--dir); ссылка на каталог разворачивается
	Output      string        // собираемый файл (--output); ссылки в пути к его каталогу разворачиваются
	Prefix      string        // префикс имён (--prefix)
	MaxSizeMB   int           // порог суммарного размера источников; <= 0 — без порога
	Refresh     bool          // пересобрать сейчас, порог не учитывается
	Version     string        // версия бинарника, входит в отпечаток
	StateDir    string        // ~/.kubeconfig-composer
	Log         io.Writer     // предупреждения; обычно os.Stderr
	LockTimeout time.Duration // сколько ждать блокировку init при первой сборке; 0 — 30 с
	// KubeconfigLockTimeout — сколько ждать lock-файл client-go "<output>.lock",
	// который держит kubectl во время записи; 0 — 1 с.
	KubeconfigLockTimeout time.Duration
}

// Result — итог Run.
type Result struct {
	Output   string // абсолютный путь kc.yaml без разворота ссылок — его печатает export KUBECONFIG
	ExportOK bool   // kc.yaml существует, можно печатать export (в том числе при ошибке)
	Rebuilt  bool   // kc.yaml был записан заново
}

// resolvePaths возвращает пути, с которыми работает init: dir — абсолютный
// каталог источников с развёрнутыми ссылками (filepath.Walk не заходит в
// корень-ссылку); output — абсолютный output, у которого развёрнут путь к
// каталогу, чтобы он совпал с путём, найденным при обходе dir; exported —
// абсолютный output как задан, для export KUBECONFIG. Несуществующий путь
// остаётся абсолютным без разворота: ошибку «нет каталога» выдаст обход.
func resolvePaths(dirArg, outputArg string) (dir, output, exported string, err error) {
	if dir, err = filepath.Abs(dirArg); err != nil {
		return "", "", "", err
	}
	if r, rerr := filepath.EvalSymlinks(dir); rerr == nil {
		dir = r
	}
	if exported, err = filepath.Abs(outputArg); err != nil {
		return "", "", "", err
	}
	output = exported
	if r, rerr := filepath.EvalSymlinks(filepath.Dir(exported)); rerr == nil {
		output = filepath.Join(r, filepath.Base(exported))
	}
	return dir, output, exported, nil
}

// Run выполняет алгоритм init: отпечаток, порог, блокировка init для пары
// (--dir, --output), пересборка (rebuild). Состояние сохраняется, только если
// пересборка дошла до конца. Подробности — в спеке docs/superpowers/specs/.
func Run(opts Options) (res Result, err error) {
	logf := func(format string, a ...any) {
		if opts.Log != nil {
			fmt.Fprintf(opts.Log, format+"\n", a...)
		}
	}

	dir, output, exported, err := resolvePaths(opts.Dir, opts.Output)
	if err != nil {
		return res, err
	}
	res.Output = exported
	defer func() { res.ExportOK = fileExists(output) }()

	snap, err := Scan(dir, output, opts.Version, opts.Prefix, dir, output)
	if err != nil {
		return res, fmt.Errorf("не удалось обойти %s: %w", dir, err)
	}

	statePath, lockPath := statePaths(opts.StateDir, dir, output)
	// prev — состояние прошлой сборки этой пары; его же получает rebuild.
	var prev State
	var hasPrev bool
	upToDate := func() bool {
		prev, hasPrev = LoadState(statePath)
		return hasPrev && prev.Fingerprint == snap.Hash && fileExists(output)
	}
	if !opts.Refresh && upToDate() {
		return res, nil
	}

	if !opts.Refresh && opts.MaxSizeMB > 0 && fileExists(output) && snap.TotalSize > int64(opts.MaxSizeMB)<<20 {
		logf("⚠ kc.yaml устарел: источники весят %.1f МБ при пороге %d МБ; запустите `kubeconfig-composer init --refresh`",
			float64(snap.TotalSize)/(1<<20), opts.MaxSizeMB)
		return res, nil
	}

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

	// Пока ждали блокировку, другой процесс мог собрать то же самое. Заодно
	// prev перечитывается: другой процесс мог его обновить.
	if !opts.Refresh && upToDate() {
		return res, nil
	}

	if snap.Files == 0 {
		logf("⚠ в %s не найдено ни одного файла-источника", dir)
	}

	var prevPtr *State
	if hasPrev {
		prevPtr = &prev
	}
	out, err := rebuild(opts, dir, output, exported, prevPtr, logf)
	if err != nil {
		return res, err
	}
	res.Rebuilt = out.wrote

	st := State{
		Fingerprint:      snap.Hash,
		Version:          opts.Version,
		Updated:          time.Now().UTC().Format(time.RFC3339),
		Names:            out.names,
		SourceNamespaces: out.sourceNamespaces,
	}
	if err := SaveState(statePath, st); err != nil {
		return res, err
	}
	return res, nil
}

// rebuildResult — итог rebuild: был ли записан файл и что сохранить в State.
type rebuildResult struct {
	wrote            bool
	names            []string          // State.Names
	sourceNamespaces map[string]string // State.SourceNamespaces
}

// maxSessionAttempts — сколько раз rebuild переносит сессию заново, если
// прежний output изменился, пока мы его читали и переносили.
const maxSessionAttempts = 3

// beforeWriteHook вызывается в rebuild после переноса сессии и перед
// перепроверкой output; тесты подменяют им запись kubectx. В проде nil.
var beforeWriteHook func()

// retrySleep — пауза перед повтором, если прежний output пуст; тесты подменяют.
var retrySleep = time.Sleep

// fileStamp — что rebuild сравнивает, чтобы заметить чужую запись в output.
type fileStamp struct {
	exists bool
	size   int64
	mtime  int64 // UnixNano
}

func stampOf(path string) fileStamp {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{exists: true, size: info.Size(), mtime: info.ModTime().UnixNano()}
}

// rebuild собирает output из источников и записывает его, если содержимое
// изменилось. prev — состояние прошлой сборки пары или nil, если его нет.
//
// Разбор источников идёт без блокировок: он долгий, а пока lock client-go
// "<exported>.lock" занят, kubectl config use-context падает сразу. Под lock
// только операции в памяти и rename. Попытка: stat output → прочитать прежний
// output (сессию; нечитаемый файл сохраняется в бэкап) → current-context (до
// Dedupe) → Dedupe → запомнить namespace источников → перенести namespace →
// бэкап, если пропадают чужие записи → stat output ещё раз. Если файл
// изменился, попытка повторяется заново с собранного из источников конфига,
// но не больше maxSessionAttempts раз; затем запись. Пустой output считается
// чужой записью в процессе (kubectx делает truncate + write): ждём и пробуем
// снова, а на последней попытке считаем, что сессии нет.
//
// kubectl не может записать файл между нашими чтением и rename: он берёт тот
// же lock. kubectx и kubens lock не берут; их запись теряется, только если
// попала между последним stat и rename.
func rebuild(opts Options, dir, output, exported string, prev *State, logf func(string, ...any)) (rebuildResult, error) {
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
		return rebuildResult{}, fmt.Errorf("не удалось собрать конфиг: %w", err)
	}
	// Каждая попытка начинает с этой копии, чтобы сессия прошлой попытки не
	// осталась в результате.
	base := c.Result().DeepCopy()

	// kubectl берёт lock по пути из KUBECONFIG, то есть по exported.
	timeout := opts.KubeconfigLockTimeout
	if timeout == 0 {
		timeout = time.Second
	}
	unlock, err := lockKubeconfig(exported, timeout)
	if err != nil {
		if errors.Is(err, errKubeconfigLockBusy) {
			logf("⚠ %s.lock занят; если kubectl не запущен, удалите его", exported)
		}
		return rebuildResult{}, err
	}
	defer unlock()

	var prevSource map[string]string
	var owned map[string]bool
	if prev != nil {
		prevSource = prev.SourceNamespaces
		owned = prev.owned()
	}
	backupDir := filepath.Join(opts.StateDir, "backups")

	var (
		unreadableSaved  bool // нечитаемый output уже сохранён в этой пересборке
		lostSaved        bool // output с пропадающими чужими записями уже сохранён
		lostBackup       string
		lostBackupErr    error
		lost             []string
		sourceNamespaces map[string]string
	)
	for attempt := 1; ; attempt++ {
		last := attempt == maxSessionAttempts
		*c.Result() = *base.DeepCopy()

		before := stampOf(output)
		empty := before.exists && before.size == 0
		if empty && !last {
			retrySleep(10 * time.Millisecond)
			continue
		}

		var sess *Session
		if !empty {
			var serr error
			if sess, serr = ReadSession(output); serr != nil {
				sess = nil
				if !unreadableSaved {
					unreadableSaved = true
					backup, berr := backupFile(output, backupDir)
					if berr != nil {
						logf("⚠ не удалось прочитать прежний %s: %v; сохранить прежний файл не удалось: %v", output, serr, berr)
					} else {
						logf("⚠ не удалось прочитать прежний %s: %v; прежний файл сохранён: %s", output, serr, backup)
					}
				}
			}
		}

		// current-context ставим до Dedupe: он не схлопывает контекст из current-context.
		if sess != nil {
			sess.ApplyCurrentContext(c.Result())
		}
		c.Dedupe()
		sourceNamespaces = contextNamespaces(c.Result())
		lost = nil
		if sess != nil {
			sess.ApplyNamespaces(c.Result(), prevSource)
			// Пропадает то, что init на прошлой сборке не записывал (или
			// состояния нет вовсе, и файл собирал не init): сохраняем прежний файл.
			lost = sess.LostForeign(c.Result(), owned)
			if len(lost) > 0 && !lostSaved {
				lostSaved = true
				lostBackup, lostBackupErr = backupFile(output, backupDir)
			}
		}

		if beforeWriteHook != nil {
			beforeWriteHook()
		}
		if last || stampOf(output) == before {
			break
		}
	}

	if len(lost) > 0 {
		list := formatLost(lost)
		if lostBackupErr != nil {
			logf("⚠ в новом %s нет записей, которых нет среди источников: %s; сохранить прежний файл не удалось: %v", output, list, lostBackupErr)
		} else {
			logf("⚠ в новом %s нет записей, которых нет среди источников: %s; прежний файл сохранён: %s", output, list, lostBackup)
		}
	}

	res := rebuildResult{names: configNames(c.Result()), sourceNamespaces: sourceNamespaces}
	if fileExists(output) {
		if same, err := c.OutputUnchanged(output); err == nil && same {
			return res, nil
		}
	}
	if err := c.WriteOutputAtomic(output); err != nil {
		return rebuildResult{}, err
	}
	res.wrote = true
	return res, nil
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
