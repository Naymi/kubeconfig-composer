package scanner

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"k8s.io/client-go/tools/clientcmd"
)

// skippedDirs — служебные каталоги в ~/.kube, где kubeconfig'ов нет: кэш
// discovery и HTTP-кэш kubectl (файлов там тысячи, у автора ~10 тысяч и 1,5 ГБ)
// и kubens/, где kubens хранит прежний namespace каждого контекста
// (~/.kube/kubens/<context>) и переписывает его при каждом переключении.
var skippedDirs = map[string]bool{"cache": true, "http-cache": true, "kubens": true}

// IsSkippedDir сообщает, нужно ли пропустить каталог name при поиске
// kubeconfig: служебные каталоги kubectl и kubens и скрытые каталоги. Корень
// обхода вызывающий код не проверяет — он сам может быть скрытым (~/.kube).
func IsSkippedDir(name string) bool {
	return skippedDirs[name] || strings.HasPrefix(name, ".")
}

// IsServiceFile сообщает, что файл name служебный и источником не бывает,
// хотя лежит рядом с kubeconfig'ами: kubectx пишет прежний контекст в
// ~/.kube/kubectx, client-go на время записи kubeconfig создаёт рядом
// <kubeconfig>.lock. Оба меняются при каждом переключении контекста.
func IsServiceFile(name string) bool {
	return name == "kubectx" || strings.HasSuffix(name, ".lock")
}

// IsKubeconfigFile проверяет, является ли файл kubeconfig
func IsKubeconfigFile(path string, info os.FileInfo) bool {
	name := info.Name()

	if strings.HasPrefix(name, ".") || IsServiceFile(name) {
		return false
	}

	if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
		return true
	}

	if name == "config" || strings.HasPrefix(name, "kubeconfig") {
		return true
	}

	// Проверяем содержимое файла
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	var data map[string]interface{}
	if err := yaml.Unmarshal(content, &data); err != nil {
		return false
	}

	_, hasContexts := data["contexts"]
	_, hasClusters := data["clusters"]
	_, hasUsers := data["users"]

	return hasContexts || hasClusters || hasUsers
}

// WalkKubeconfigFiles — единственный обход каталога источников, через него
// работают FindKubeconfigFiles, Composer.ScanDirectory и команда list. visit
// вызывается для каждого файла, который проходит IsKubeconfigFile; подкаталоги
// из IsSkippedDir пропускаются (корень не проверяется — ~/.kube сам скрытый).
//
// Ошибкой заканчивается только недоступный корень. Недоступный файл или
// подкаталог внутри передаётся в onSkip (если он не nil) и пропускается: иначе
// одна нечитаемая папка ломала бы каждую сборку, а обход для отпечатка init
// (initstate.Scan) на ней не останавливается. Ошибка из visit прерывает обход.
func WalkKubeconfigFiles(dir string, onSkip func(path string, err error), visit func(path string) error) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if path == dir {
				return err
			}
			if onSkip != nil {
				onSkip(path, err)
			}
			if info != nil && info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			if path != dir && IsSkippedDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		if !IsKubeconfigFile(path, info) {
			return nil
		}
		return visit(path)
	})
}

// FindKubeconfigFiles возвращает kubeconfig файлы в dir, которые загружаются
// через clientcmd; остальные молча отбрасываются.
func FindKubeconfigFiles(dir string) ([]string, error) {
	var files []string

	err := WalkKubeconfigFiles(dir, nil, func(path string) error {
		if _, err := clientcmd.LoadFromFile(path); err != nil {
			return nil
		}
		files = append(files, path)
		return nil
	})

	return files, err
}
