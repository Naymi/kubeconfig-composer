package cleaner

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// RemoveContextsFromFile удаляет указанные контексты из kubeconfig файла
func RemoveContextsFromFile(filePath string, contextsToRemove []string) error {
	config, err := clientcmd.LoadFromFile(filePath)
	if err != nil {
		return fmt.Errorf("не удалось загрузить файл: %w", err)
	}

	removeSet := make(map[string]bool)
	for _, ctx := range contextsToRemove {
		removeSet[ctx] = true
	}

	// Удаляем контексты
	for contextName := range config.Contexts {
		if removeSet[contextName] {
			delete(config.Contexts, contextName)
		}
	}

	// Удаляем неиспользуемые кластеры и пользователей
	usedClusters := make(map[string]bool)
	usedUsers := make(map[string]bool)
	for _, context := range config.Contexts {
		usedClusters[context.Cluster] = true
		usedUsers[context.AuthInfo] = true
	}

	for clusterName := range config.Clusters {
		if !usedClusters[clusterName] {
			delete(config.Clusters, clusterName)
		}
	}

	for userName := range config.AuthInfos {
		if !usedUsers[userName] {
			delete(config.AuthInfos, userName)
		}
	}

	// Если текущий контекст был удалён, сбрасываем его
	if removeSet[config.CurrentContext] {
		config.CurrentContext = ""
	}

	return WriteConfig(filePath, config)
}

// WriteConfig записывает конфигурацию в файл
func WriteConfig(filePath string, config *clientcmdapi.Config) error {
	data, err := clientcmd.Write(*config)
	if err != nil {
		return fmt.Errorf("не удалось сериализовать конфигурацию: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0600); err != nil {
		return fmt.Errorf("не удалось записать файл: %w", err)
	}

	return nil
}

// WriteFileAtomic записывает data в path так, что читатель (например, kubectl
// в другом терминале) видит либо старое, либо новое содержимое целиком:
// сначала временный файл рядом, потом rename. Если path — символическая
// ссылка, обновляется её цель, а не сама ссылка. Недостающие каталоги создаются.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("не удалось создать каталог %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("не удалось создать временный файл: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("не удалось записать временный файл: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("не удалось выставить права: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("не удалось закрыть временный файл: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("не удалось заменить %s: %w", path, err)
	}
	return nil
}
