package initstate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// lockKubeconfig берёт блокировку kubeconfig по протоколу client-go
// (clientcmd.lockFile): файл "<path>.lock", созданный с O_EXCL. Её же берёт
// kubectl, когда пишет kubeconfig (`config use-context`, `set-context`), так
// что пока она у нас, kubectl не запишет файл между нашими чтением и rename.
// Занятую блокировку опрашивает каждые 10 мс, но не дольше timeout. Чужой
// lock-файл не удаляется никогда. unlock удаляет файл, как client-go.
// O_EXCL поддерживается и на windows, build-тегов не нужно.
func lockKubeconfig(path string, timeout time.Duration) (unlock func(), err error) {
	lockPath := path + ".lock"
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
			return nil, fmt.Errorf("%s занят дольше %s", lockPath, timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
