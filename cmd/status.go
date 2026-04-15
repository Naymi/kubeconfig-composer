package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/naymi/kubeconfig-composer/pkg/checker"
	"github.com/naymi/kubeconfig-composer/pkg/scanner"
	"github.com/spf13/cobra"
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


func runStatus(cmd *cobra.Command, args []string) error {
	var kubeconfigFiles []string

	if statusAll {
		// Сканируем директорию для поиска всех kubeconfig файлов
		fmt.Printf("Сканирование директории %s...\n", statusDir)
		files, err := scanner.FindKubeconfigFiles(statusDir)
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

	allStatuses := make([]checker.ClusterStatus, 0)
	statusChan := make(chan checker.ClusterStatus, 100)
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
				status := checker.CheckClusterStatus(kPath, cName, cInfo, statusTimeout)
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

