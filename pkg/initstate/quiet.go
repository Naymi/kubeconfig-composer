package initstate

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/pterm/pterm"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// CaptureStdout запускает fn, перехватывая всё, что она печатает в os.Stdout
// (fmt.Print*, tablewriter) и через pterm. Composer печатает в stdout, а stdout
// команды init уходит в eval, так что любой посторонний вывод выполнился бы как
// shell-код. Строки с «⚠» (без ANSI-кодов) возвращаются как предупреждения,
// остальное отбрасывается. Функция меняет глобальный os.Stdout и поэтому не
// потокобезопасна: вызывать из одной горутины.
func CaptureStdout(fn func() error) (warnings []string, err error) {
	r, w, perr := os.Pipe()
	if perr != nil {
		return nil, fmt.Errorf("не удалось перехватить вывод: %w", perr)
	}

	orig := os.Stdout
	os.Stdout = w
	pterm.SetDefaultOutput(w)

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		io.Copy(&buf, r)
		close(done)
	}()

	defer func() {
		w.Close()
		<-done
		r.Close()
		os.Stdout = orig
		pterm.SetDefaultOutput(orig)
		for _, line := range strings.Split(buf.String(), "\n") {
			line = strings.TrimSpace(ansiRe.ReplaceAllString(line, ""))
			if strings.Contains(line, "⚠") {
				warnings = append(warnings, line)
			}
		}
	}()

	return nil, fn()
}
