//go:build windows

package initstate

// Lock на windows пустой: init рассчитан на bash/zsh, а релизы под windows
// только должны собираться.
type Lock struct{}

// TryLock всегда успешен.
func TryLock(path string) (*Lock, error) { return &Lock{}, nil }

// Unlock ничего не делает.
func (l *Lock) Unlock() {}
