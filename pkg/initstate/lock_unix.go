//go:build !windows

package initstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Lock — эксклюзивная блокировка файла (flock). Освобождается Unlock или
// автоматически при завершении процесса.
type Lock struct{ f *os.File }

// TryLock берёт блокировку без ожидания. Если её держит другой процесс,
// возвращает (nil, nil).
func TryLock(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("не удалось создать каталог блокировки: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть файл блокировки: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, nil
		}
		return nil, fmt.Errorf("flock: %w", err)
	}
	return &Lock{f: f}, nil
}

// Unlock снимает блокировку.
func (l *Lock) Unlock() {
	syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	l.f.Close()
}
