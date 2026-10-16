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

// FindKubeconfigFiles сканирует директорию и возвращает список kubeconfig
// файлов. Ошибкой заканчивается только недоступный корень; недоступный файл
// или подкаталог внутри молча пропускается, как и файл, который не загружается.
func FindKubeconfigFiles(dir string) ([]string, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if path == dir {
				return err
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

		// Проверяем, что файл можно загрузить как kubeconfig
		_, err = clientcmd.LoadFromFile(path)
		if err != nil {
			return nil
		}

		files = append(files, path)
		return nil
	})

	return files, err
}
