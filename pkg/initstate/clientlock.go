package initstate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// errKubeconfigLockBusy — lock-файл client-go так и не освободился за timeout.
// Только в этом случае есть смысл советовать удалить его вручную.
var errKubeconfigLockBusy = errors.New("занят")

// lockKubeconfig берёт блокировку kubeconfig по протоколу client-go
// (clientcmd.lockFile): создаёт недостающий каталог и файл "<path>.lock" с
// O_EXCL. Её же берёт kubectl, когда пишет kubeconfig (`config use-context`,
// `set-context`), так что пока она у нас, kubectl не запишет файл между нашими
// чтением и rename. kubectx и kubens этот lock не берут — от них rebuild
// защищается перепроверкой файла перед записью. Занятую блокировку опрашивает
// каждые 10 мс, но не дольше timeout, после чего возвращает ошибку,
// оборачивающую errKubeconfigLockBusy; любая другая ошибка (каталог не создать,
// нет прав) возвращается как есть. Чужой lock-файл не удаляется никогда.
// unlock удаляет файл, как client-go. O_EXCL поддерживается и на windows,
// build-тегов не нужно.
func lockKubeconfig(path string, timeout time.Duration) (unlock func(), err error) {
	lockPath := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return func() { os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("не удалось создать %s: %w", lockPath, err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s %w дольше %s", lockPath, errKubeconfigLockBusy, timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
