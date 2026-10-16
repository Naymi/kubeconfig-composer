package initstate

import (
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"testing"

	"github.com/pterm/pterm"
)

func TestCaptureStdoutSwallowsOutputAndKeepsWarnings(t *testing.T) {
	// Подменяем настоящий stdout на свою трубу и проверяем, что в неё ничего не попало.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	sentinel := errors.New("boom")
	warnings, gotErr := CaptureStdout(func() error {
		fmt.Println("шум через fmt")
		fmt.Printf("→ Автоматически принято: \033[33mx\033[0m\n")
		pterm.FgYellow.Println("⚠ предупреждение pterm")
		return sentinel
	})

	if os.Stdout != w {
		t.Error("os.Stdout не восстановлен")
	}
	w.Close()
	leaked, _ := io.ReadAll(r)
	if len(leaked) != 0 {
		t.Errorf("в настоящий stdout просочилось: %q", leaked)
	}
	if gotErr != sentinel {
		t.Errorf("ошибка fn потеряна: %v", gotErr)
	}
	want := []string{"⚠ предупреждение pterm"}
	if !reflect.DeepEqual(warnings, want) {
		t.Errorf("warnings = %q, want %q", warnings, want)
	}
}
