package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/naymi/kubeconfig-composer/pkg/checker"
	"github.com/naymi/kubeconfig-composer/pkg/cleaner"
	"github.com/naymi/kubeconfig-composer/pkg/scanner"
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
		fmt.Printf("Сканирование директории %s...\n", cleanupDir)
		files, err := scanner.FindKubeconfigFiles(cleanupDir)
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
		kubeconfigFiles = []string{cleanupKubeconfig}
	}

	results := make([]cleanupResult, 0)
	resultChan := make(chan cleanupResult, len(kubeconfigFiles))
	semaphore := make(chan struct{}, 20) // Ограничение на 20 одновременных проверок

	// Анализируем каждый файл параллельно
	for _, kubeconfigPath := range kubeconfigFiles {
		go func(kPath string) {
			config, err := clientcmd.LoadFromFile(kPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  Не удалось загрузить %s: %v\n", kPath, err)
				resultChan <- cleanupResult{}
				return
			}

			if len(config.Contexts) == 0 {
				resultChan <- cleanupResult{}
				return
			}

			fmt.Printf("Проверка %s (%d контекстов)...\n", kPath, len(config.Contexts))

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
		fmt.Println("\n✓ Все контексты доступны, очистка не требуется")
		return nil
	}

	// Показываем что будет удалено
	fmt.Println("\n" + strings.Repeat("=", 70))
	fmt.Println("Результаты анализа:")
	fmt.Println(strings.Repeat("=", 70))

	filesToDelete := 0
	contextsToDelete := 0

	defaultConfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	absDefault, _ := filepath.Abs(defaultConfig)

	for _, result := range results {
		absPath, _ := filepath.Abs(result.filePath)
		isDefaultConfig := absPath == absDefault

		if result.shouldDeleteFile {
			filesToDelete++
			fmt.Printf("\n❌ Файл будет удалён: %s\n", result.filePath)
			fmt.Printf("   Причина: все %d контекстов недоступны\n", result.totalContexts)
			for _, ctx := range result.unavailableContexts {
				fmt.Printf("   - %s\n", ctx)
			}
		} else if len(result.unavailableContexts) == result.totalContexts && isDefaultConfig {
			contextsToDelete += len(result.unavailableContexts)
			fmt.Printf("\n🧹 Файл: %s (защищён от удаления)\n", result.filePath)
			fmt.Printf("   Все %d контекстов недоступны и будут удалены:\n", result.totalContexts)
			for _, ctx := range result.unavailableContexts {
				fmt.Printf("   - %s\n", ctx)
			}
			fmt.Printf("   ⚠️  Файл останется пустым\n")
		} else {
			contextsToDelete += len(result.unavailableContexts)
			fmt.Printf("\n🧹 Файл: %s\n", result.filePath)
			fmt.Printf("   Недоступные контексты (%d из %d):\n", len(result.unavailableContexts), result.totalContexts)
			for _, ctx := range result.unavailableContexts {
				fmt.Printf("   - %s\n", ctx)
			}
			fmt.Printf("   Доступные контексты (%d): %s\n", len(result.availableContexts), strings.Join(result.availableContexts, ", "))
		}
	}

	fmt.Println("\n" + strings.Repeat("=", 70))
	fmt.Printf("Итого: %d файлов будет удалено, %d контекстов будет удалено\n", filesToDelete, contextsToDelete)
	fmt.Println(strings.Repeat("=", 70))

	if cleanupDryRun {
		fmt.Println("\n🔍 Режим dry-run: изменения не применены")
		return nil
	}

	// Запрашиваем подтверждение
	if !cleanupNoConfirm {
		fmt.Print("\nПродолжить удаление? (yes/no): ")
		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("ошибка чтения ввода: %w", err)
		}
		input = strings.TrimSpace(strings.ToLower(input))
		if input != "yes" && input != "y" {
			fmt.Println("Отменено")
			return nil
		}
	}

	// Выполняем очистку
	fmt.Println("\nВыполнение очистки...")

	for _, result := range results {
		if result.shouldDeleteFile {
			if err := os.Remove(result.filePath); err != nil {
				fmt.Fprintf(os.Stderr, "❌ Не удалось удалить %s: %v\n", result.filePath, err)
			} else {
				fmt.Printf("✓ Удалён файл: %s\n", result.filePath)
			}
		} else {
			if err := cleaner.RemoveContextsFromFile(result.filePath, result.unavailableContexts); err != nil {
				fmt.Fprintf(os.Stderr, "❌ Не удалось очистить %s: %v\n", result.filePath, err)
			} else {
				fmt.Printf("✓ Удалено %d контекстов из %s\n", len(result.unavailableContexts), result.filePath)
			}
		}
	}

	fmt.Println("\n✓ Очистка завершена")
	return nil
}
