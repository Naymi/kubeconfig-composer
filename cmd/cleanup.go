package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/naymi/kubeconfig-composer/pkg/checker"
	"github.com/naymi/kubeconfig-composer/pkg/cleaner"
	"github.com/naymi/kubeconfig-composer/pkg/scanner"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	cleanupKubeconfig string
	cleanupTimeout    int
	cleanupAll        bool
	cleanupDir        string
	cleanupDryRun     bool
	cleanupNoConfirm  bool
)

var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Удалить недоступные контексты и kubeconfig файлы",
	Long: `Проверяет доступность кластеров и удаляет недоступные контексты
из kubeconfig файлов или полностью удаляет файлы без доступных контекстов.`,
	Example: `  # Очистить ~/.kube/config от недоступных контекстов
  kubeconfig-composer cleanup

  # Очистить конкретный файл
  kubeconfig-composer cleanup --kubeconfig ~/.kube/merged-config.yaml

  # Очистить все kubeconfig файлы в ~/.kube
  kubeconfig-composer cleanup --all

  # Предварительный просмотр без удаления
  kubeconfig-composer cleanup --all --dry-run

  # Без подтверждения
  kubeconfig-composer cleanup --all --yes`,
	RunE: runCleanup,
}

func init() {
	rootCmd.AddCommand(cleanupCmd)

	defaultKubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	defaultDir := filepath.Join(os.Getenv("HOME"), ".kube")

	cleanupCmd.Flags().StringVarP(&cleanupKubeconfig, "kubeconfig", "k", defaultKubeconfig, "Путь к kubeconfig файлу")
	cleanupCmd.Flags().IntVarP(&cleanupTimeout, "timeout", "t", 3, "Таймаут подключения в секундах")
	cleanupCmd.Flags().BoolVarP(&cleanupAll, "all", "a", false, "Очистить все kubeconfig файлы в директории")
	cleanupCmd.Flags().StringVarP(&cleanupDir, "dir", "d", defaultDir, "Директория для поиска kubeconfig файлов (используется с --all)")
	cleanupCmd.Flags().BoolVar(&cleanupDryRun, "dry-run", false, "Показать что будет удалено без фактического удаления")
	cleanupCmd.Flags().BoolVarP(&cleanupNoConfirm, "yes", "y", false, "Не запрашивать подтверждение")
}

type cleanupResult struct {
	filePath           string
	totalContexts      int
	unavailableContexts []string
	availableContexts   []string
	shouldDeleteFile   bool
}

func runCleanup(cmd *cobra.Command, args []string) error {
	var kubeconfigFiles []string

	if cleanupAll {
		files, err := scanner.FindKubeconfigFiles(cleanupDir)
		if err != nil {
			return fmt.Errorf("не удалось просканировать директорию: %w", err)
		}
		if len(files) == 0 {
			fmt.Println("Kubeconfig файлы не найдены")
			return nil
		}
		kubeconfigFiles = files
		fmt.Printf("Найдено %d файлов\n", len(files))
	} else {
		kubeconfigFiles = []string{cleanupKubeconfig}
	}

	results := make([]cleanupResult, 0)
	resultChan := make(chan cleanupResult, len(kubeconfigFiles))
	semaphore := make(chan struct{}, 20) // Ограничение на 20 одновременных проверок

	// Подсчитываем общее количество контекстов
	totalContexts := 0
	for _, kubeconfigPath := range kubeconfigFiles {
		config, err := clientcmd.LoadFromFile(kubeconfigPath)
		if err != nil {
			continue
		}
		totalContexts += len(config.Contexts)
	}

	fmt.Printf("Анализ %d контекстов...\n", totalContexts)

	// Анализируем каждый файл параллельно
	for _, kubeconfigPath := range kubeconfigFiles {
		go func(kPath string) {
			config, err := clientcmd.LoadFromFile(kPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "⚠ Не удалось загрузить %s: %v\n", kPath, err)
				resultChan <- cleanupResult{}
				return
			}

			if len(config.Contexts) == 0 {
				resultChan <- cleanupResult{}
				return
			}

			result := cleanupResult{
				filePath:           kPath,
				totalContexts:      len(config.Contexts),
				unavailableContexts: make([]string, 0),
				availableContexts:   make([]string, 0),
			}

			// Канал для параллельной проверки контекстов внутри файла
			type contextCheck struct {
				name      string
				available bool
			}
			checkChan := make(chan contextCheck, len(config.Contexts))

			// Проверяем доступность каждого контекста параллельно
			for contextName, contextInfo := range config.Contexts {
				semaphore <- struct{}{} // Захватываем слот
				go func(cName string, clusterName string) {
					defer func() { <-semaphore }() // Освобождаем слот
					status := checker.CheckClusterStatus(kPath, cName, clusterName, cleanupTimeout)
					checkChan <- contextCheck{
						name:      cName,
						available: status.Status == "✓" || status.Status == "⚠",
					}
				}(contextName, contextInfo.Cluster)
			}

			// Собираем результаты проверок контекстов
			for i := 0; i < len(config.Contexts); i++ {
				check := <-checkChan
				if check.available {
					result.availableContexts = append(result.availableContexts, check.name)
				} else {
					result.unavailableContexts = append(result.unavailableContexts, check.name)
				}
			}
			close(checkChan)

			// Если все контексты недоступны, помечаем файл для удаления
			// Но не удаляем ~/.kube/config - только очищаем его
			defaultConfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
			if len(result.availableContexts) == 0 {
				absPath, _ := filepath.Abs(kPath)
				absDefault, _ := filepath.Abs(defaultConfig)
				if absPath == absDefault {
					result.shouldDeleteFile = false
				} else {
					result.shouldDeleteFile = true
				}
			}

			resultChan <- result
		}(kubeconfigPath)
	}

	// Собираем результаты по всем файлам
	for i := 0; i < len(kubeconfigFiles); i++ {
		result := <-resultChan
		if len(result.unavailableContexts) > 0 {
			results = append(results, result)
		}
	}
	close(resultChan)

	if len(results) == 0 {
		fmt.Println("\n✓ Все контексты доступны")
		return nil
	}

	// Показываем что будет удалено
	fmt.Println()

	const (
		red    = "\033[31m"
		green  = "\033[32m"
		yellow = "\033[33m"
		cyan   = "\033[36m"
		reset  = "\033[0m"
	)

	filesToDelete := 0
	contextsToDelete := 0

	defaultConfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	absDefault, _ := filepath.Abs(defaultConfig)

	for _, result := range results {
		absPath, _ := filepath.Abs(result.filePath)
		isDefaultConfig := absPath == absDefault

		if result.shouldDeleteFile {
			filesToDelete++
			fmt.Printf("%s✗ %s%s\n", red, result.filePath, reset)
			for _, ctx := range result.unavailableContexts {
				fmt.Printf("%s  ✗ %s%s\n", red, ctx, reset)
			}
		} else if len(result.unavailableContexts) == result.totalContexts && isDefaultConfig {
			contextsToDelete += len(result.unavailableContexts)
			fmt.Printf("%s⚠ %s%s\n", yellow, result.filePath, reset)
			for _, ctx := range result.unavailableContexts {
				fmt.Printf("%s  ✗ %s%s\n", yellow, ctx, reset)
			}
		} else {
			contextsToDelete += len(result.unavailableContexts)
			fmt.Printf("%s○ %s%s\n", cyan, result.filePath, reset)
			for _, ctx := range result.unavailableContexts {
				fmt.Printf("%s  ✗ %s%s\n", red, ctx, reset)
			}
			for _, ctx := range result.availableContexts {
				fmt.Printf("%s  ✓ %s%s\n", green, ctx, reset)
			}
		}
	}

	fmt.Printf("\n%d файлов • %d контекстов\n", filesToDelete, contextsToDelete)

	if cleanupDryRun {
		fmt.Println("Режим dry-run: изменения не применены")
		return nil
	}

	// Запрашиваем подтверждение
	if !cleanupNoConfirm {
		result, _ := pterm.DefaultInteractiveConfirm.
			WithDefaultText("Продолжить?").
			WithDefaultValue(false).
			Show()
		if !result {
			fmt.Println("Отменено")
			return nil
		}
	}

	// Выполняем очистку
	fmt.Println()

	for _, result := range results {
		if result.shouldDeleteFile {
			if err := os.Remove(result.filePath); err != nil {
				pterm.FgRed.Printf("✗ Не удалось удалить %s: %v\n", result.filePath, err)
			} else {
				pterm.FgGreen.Printf("✓ Удалён: %s\n", result.filePath)
			}
		} else {
			if err := cleaner.RemoveContextsFromFile(result.filePath, result.unavailableContexts); err != nil {
				pterm.FgRed.Printf("✗ Не удалось очистить %s: %v\n", result.filePath, err)
			} else {
				pterm.FgGreen.Printf("✓ Удалено %d контекстов из %s\n", len(result.unavailableContexts), result.filePath)
			}
		}
	}

	fmt.Println()
	pterm.FgGreen.Println("✓ Готово")
	return nil
}
