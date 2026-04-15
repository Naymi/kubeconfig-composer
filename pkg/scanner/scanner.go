package scanner

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"k8s.io/client-go/tools/clientcmd"
)

// IsKubeconfigFile проверяет, является ли файл kubeconfig
func IsKubeconfigFile(path string, info os.FileInfo) bool {
	name := info.Name()

	if strings.HasPrefix(name, ".") {
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

// FindKubeconfigFiles сканирует директорию и возвращает список kubeconfig файлов
func FindKubeconfigFiles(dir string) ([]string, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
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
