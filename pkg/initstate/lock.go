package initstate

import (
	"fmt"
	"time"
)

// WaitLock ждёт блокировку, опрашивая TryLock каждые 50 мс, но не дольше
// timeout — зависший процесс не должен подвесить старт терминала навсегда.
func WaitLock(path string, timeout time.Duration) (*Lock, error) {
	deadline := time.Now().Add(timeout)
	for {
		l, err := TryLock(path)
		if err != nil {
			return nil, err
		}
		if l != nil {
			return l, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("не удалось получить блокировку %s за %s", path, timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
