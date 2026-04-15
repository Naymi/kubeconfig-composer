package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	statusKubeconfig string
	statusTimeout    int
	statusAll        bool
	statusDir        string
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Проверить статус подключения к кластерам",
	Long: `Проходит по каждому контексту в kubeconfig и проверяет
возможность подключения к кластеру, выводя результаты в виде таблицы.`,
	Example: `  # Проверить статус для ~/.kube/config
  kubeconfig-composer status

  # Проверить статус для конкретного файла
  kubeconfig-composer status --kubeconfig ~/.kube/merged-config.yaml

  # Проверить все kubeconfig файлы в ~/.kube
  kubeconfig-composer status --all

  # Проверить все kubeconfig файлы в другой директории
  kubeconfig-composer status --all --dir /path/to/configs

  # Установить таймаут подключения (в секундах)
  kubeconfig-composer status --timeout 5`,
	RunE: runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)

	defaultKubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	defaultDir := filepath.Join(os.Getenv("HOME"), ".kube")

	statusCmd.Flags().StringVarP(&statusKubeconfig, "kubeconfig", "k", defaultKubeconfig, "Путь к kubeconfig файлу")
	statusCmd.Flags().IntVarP(&statusTimeout, "timeout", "t", 3, "Таймаут подключения в секундах")
	statusCmd.Flags().BoolVarP(&statusAll, "all", "a", false, "Проверить все kubeconfig файлы в директории")
	statusCmd.Flags().StringVarP(&statusDir, "dir", "d", defaultDir, "Директория для поиска kubeconfig файлов (используется с --all)")
}

type clusterStatus struct {
	Context string
	Cluster string
	Status  string
	Version string
	Error   string
}

func runStatus(cmd *cobra.Command, args []string) error {
	var kubeconfigFiles []string

	if statusAll {
		// Сканируем директорию для поиска всех kubeconfig файлов
		fmt.Printf("Сканирование директории %s...\n", statusDir)
		files, err := findKubeconfigFiles(statusDir)
		if err != nil {
			return fmt.Errorf("не удалось просканировать директорию: %w", err)
		}
		if len(files) == 0 {
			fmt.Println("Kubeconfig файлы не найдены")
			return nil
		}
		kubeconfigFiles = files
		fmt.Printf("Найдено %d kubeconfig файлов\n\n", len(files))
	} else {
		// Проверяем один файл
		kubeconfigFiles = []string{statusKubeconfig}
	}

	allStatuses := make([]clusterStatus, 0)
	statusChan := make(chan clusterStatus, 100)
	semaphore := make(chan struct{}, 20) // Ограничение на 20 одновременных проверок
	totalContexts := 0

	// Проверяем каждый файл
	for _, kubeconfigPath := range kubeconfigFiles {
		// Загружаем kubeconfig
		config, err := clientcmd.LoadFromFile(kubeconfigPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Не удалось загрузить %s: %v\n", kubeconfigPath, err)
			continue
		}

		if len(config.Contexts) == 0 {
			continue
		}

		totalContexts += len(config.Contexts)

		if statusAll {
			fmt.Printf("Проверка %s (%d контекстов)...\n", kubeconfigPath, len(config.Contexts))
		} else {
			fmt.Printf("Проверка подключения к %d кластерам (таймаут: %ds)...\n\n", len(config.Contexts), statusTimeout)
		}

		// Запускаем параллельные проверки для всех контекстов
		for contextName, contextInfo := range config.Contexts {
			semaphore <- struct{}{} // Захватываем слот
			go func(kPath, cName, cInfo string) {
				defer func() { <-semaphore }() // Освобождаем слот
				status := checkClusterStatus(kPath, cName, cInfo)
				if statusAll {
					status.Context = fmt.Sprintf("%s (%s)", cName, filepath.Base(kPath))
				}
				statusChan <- status
			}(kubeconfigPath, contextName, contextInfo.Cluster)
		}
	}

	// Собираем результаты
	for i := 0; i < totalContexts; i++ {
		allStatuses = append(allStatuses, <-statusChan)
	}
	close(statusChan)

	if len(allStatuses) == 0 {
		fmt.Println("Контексты не найдены")
		return nil
	}

	// Выводим таблицу
	fmt.Println()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "КОНТЕКСТ\tКЛАСТЕР\tСТАТУС\tВЕРСИЯ\tОШИБКА")
	fmt.Fprintln(w, "--------\t-------\t------\t------\t------")

	successCount := 0
	for _, s := range allStatuses {
		if s.Status == "✓" {
			successCount++
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			s.Context,
			s.Cluster,
			s.Status,
			s.Version,
			s.Error,
		)
	}
	w.Flush()

	fmt.Printf("\nИтого: %d/%d кластеров доступны\n", successCount, len(allStatuses))
	return nil
}

func checkClusterStatus(kubeconfigPath, contextName, clusterName string) clusterStatus {
	status := clusterStatus{
		Context: contextName,
		Cluster: clusterName,
		Status:  "✗",
		Version: "-",
		Error:   "",
	}

	// Создаём конфигурацию для конкретного контекста
	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath}
	configOverrides := &clientcmd.ConfigOverrides{CurrentContext: contextName}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	restConfig, err := kubeConfig.ClientConfig()
	if err != nil {
		status.Error = truncateError(err.Error())
		return status
	}

	// Устанавливаем таймаут
	restConfig.Timeout = time.Duration(statusTimeout) * time.Second

	// Создаём клиент
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		status.Error = truncateError(err.Error())
		return status
	}

	// Пробуем получить версию сервера
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(statusTimeout)*time.Second)
	defer cancel()

	versionInfo, err := clientset.Discovery().ServerVersion()
	if err != nil {
		status.Error = truncateError(err.Error())
		return status
	}

	// Проверяем, что можем получить список нод (базовая проверка доступа)
	_, err = clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		// Если не можем получить ноды, но версия получена - всё равно считаем успехом
		// (может быть ограничение прав доступа)
		status.Status = "⚠"
		status.Version = versionInfo.GitVersion
		status.Error = "ограниченный доступ"
		return status
	}

	status.Status = "✓"
	status.Version = versionInfo.GitVersion
	return status
}

func truncateError(err string) string {
	// Убираем лишние детали из ошибки
	err = strings.ReplaceAll(err, "\n", " ")

	// Извлекаем основную причину
	if idx := strings.Index(err, "connection refused"); idx != -1 {
		return "connection refused"
	}
	if idx := strings.Index(err, "timeout"); idx != -1 {
		return "timeout"
	}
	if idx := strings.Index(err, "no such host"); idx != -1 {
		return "no such host"
	}
	if idx := strings.Index(err, "certificate"); idx != -1 {
		return "certificate error"
	}
	if idx := strings.Index(err, "unauthorized"); idx != -1 {
		return "unauthorized"
	}
	if idx := strings.Index(err, "forbidden"); idx != -1 {
		return "forbidden"
	}

	// Обрезаем длинные ошибки
	if len(err) > 50 {
		return err[:47] + "..."
	}
	return err
}

func findKubeconfigFiles(dir string) ([]string, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		if !isKubeconfigFile(path, info) {
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

