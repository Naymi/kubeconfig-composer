package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/naymi/kubeconfig-composer/pkg/composer"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var (
	kubeDir      string
	files        string
	output       string
	autoAccept   bool
	cleanupAfter bool
	namePrefix   string
)

var mergeCmd = &cobra.Command{
	Use:   "merge",
	Short: "Объединить kubeconfig файлы",
	Long: `Объединяет несколько kubeconfig файлов в один, автоматически
разрешая конфликты имён контекстов, кластеров и пользователей.`,
	Example: `  # Сканировать ~/.kube и создать merged-kubeconfig.yaml
  kubeconfig-composer merge

  # Указать другую директорию
  kubeconfig-composer merge --dir /path/to/configs

  # Указать конкретные файлы
  kubeconfig-composer merge --files ~/.kube/config,~/.kube/dev.yaml

  # Указать выходной файл
  kubeconfig-composer merge --output ~/.kube/merged-config

  # Автоматически принять все предложенные имена без запроса
  kubeconfig-composer merge -y

  # Объединить и переместить исходные файлы в trash
  kubeconfig-composer merge --cleanup

  # Задать свой префикс имён (по умолчанию "kc"), пустая строка отключает его.
  # Имя каждой сущности строится как "<префикс>:<имя>:<путь-от---dir>",
  # например "kc:prod:nested/config.yaml" или "kc:staging:staging.conf"
  kubeconfig-composer merge --prefix "src"`,
	RunE: runMerge,
}

func init() {
	rootCmd.AddCommand(mergeCmd)

	mergeCmd.Flags().StringVarP(&kubeDir, "dir", "d", filepath.Join(os.Getenv("HOME"), ".kube"), "Директория для сканирования kubeconfig файлов")
	mergeCmd.Flags().StringVarP(&files, "files", "f", "", "Список kubeconfig файлов через запятую")
	mergeCmd.Flags().StringVarP(&output, "output", "o", filepath.Join(os.Getenv("HOME"), ".kube", "config"), "Путь к выходному файлу")
	mergeCmd.Flags().BoolVarP(&autoAccept, "auto-accept", "y", false, "Автоматически принимать предложенные имена при конфликтах")
	mergeCmd.Flags().BoolVar(&cleanupAfter, "cleanup", false, "Переместить объединённые файлы в .kubeconfig-composer/trash")
	mergeCmd.Flags().StringVar(&namePrefix, "prefix", composer.DefaultNamePrefix, `Префикс имени каждой смерженной сущности, формат "<префикс>:<имя>:<путь>" (пустая строка отключает префикс)`)
}

func runMerge(cmd *cobra.Command, args []string) error {
	c := composer.New(autoAccept)
	c.SetBaseDir(kubeDir)
	c.SetNamePrefix(namePrefix)

	absOutput, _ := filepath.Abs(output)

	// Шаг 0: если выходной файл уже существует — берём его как базу. Его записи
	// грузятся первыми, поэтому их имена сохраняются и имеют приоритет, а сам
	// файл больше не подхватывается при сканировании (см. exclude ниже).
	if _, err := os.Stat(output); err == nil {
		if err := c.LoadFiles([]string{output}); err != nil {
			return fmt.Errorf("не удалось загрузить существующий конфиг %s: %w", output, err)
		}
		fmt.Printf("База: \033[36m%s\033[0m (существующий конфиг)\n", filepath.Base(output))
	}

	// Шаг 1: Загрузка файлов
	if files != "" {
		var filePaths []string
		for _, path := range strings.Split(files, ",") {
			path = strings.TrimSpace(path)
			if path == "" {
				continue
			}
			// Пропускаем выходной файл — он уже загружен как база.
			if abs, err := filepath.Abs(path); err == nil && abs == absOutput {
				continue
			}
			filePaths = append(filePaths, path)
		}
		fmt.Printf("Загрузка %d файлов...\n", len(filePaths))
		if err := c.LoadFiles(filePaths); err != nil {
			return fmt.Errorf("не удалось загрузить файлы: %w", err)
		}
	} else {
		fmt.Printf("Сканирование %s...\n", kubeDir)
		if err := c.ScanDirectory(kubeDir, output); err != nil {
			return fmt.Errorf("не удалось просканировать директорию: %w", err)
		}
		loadedFiles := c.GetLoadedFiles()
		fmt.Printf("Найдено %d файлов:\n", len(loadedFiles))
		for _, f := range loadedFiles {
			configName := filepath.Base(f.GetFullPath())
			fmt.Printf("  • \033[36m%s\033[0m\n", configName)
		}
	}

	// Шаг 2: Объединение конфигураций
	fmt.Println("Объединение...")
	if err := c.Merge(); err != nil {
		return fmt.Errorf("не удалось объединить конфигурации: %w", err)
	}

	// Показываем таблицу принятых изменений
	c.ShowAppliedChanges()

	// Шаг 2.5: Убираем дубликаты по содержимому (в т.ч. записи под разными
	// именами и контексты, отличающиеся только namespace). Делает результат
	// повторного merge идемпотентным.
	if report := c.Dedupe(); report.Changed() {
		pterm.FgYellow.Printf("🧹 Убрано дубликатов (контекстов: %d)\n", report.TotalRemovedContexts())
	}

	// Шаг 3: Проверка существования файла и создание бэкапа
	if _, err := os.Stat(output); err == nil {
		// Если результат ничем не отличается от уже записанного файла —
		// перезапись не нужна: не спрашиваем подтверждение, не создаём бэкап,
		// просто выходим с exit code 0.
		unchanged, err := c.OutputUnchanged(output)
		if err != nil {
			return fmt.Errorf("не удалось сравнить с существующим конфигом %s: %w", output, err)
		}
		if unchanged {
			pterm.FgGreen.Printf("✓ %s уже актуален, изменений нет\n", output)
			return nil
		}

		// Файл существует и отличается, спрашиваем подтверждение
		timestamp := time.Now().Format("20060102-150405")

		// Создаём директорию для бэкапов
		backupDir := filepath.Join(os.Getenv("HOME"), ".kubeconfig-composer", "backups")
		if err := os.MkdirAll(backupDir, 0755); err != nil {
			return fmt.Errorf("не удалось создать директорию для бэкапов: %w", err)
		}

		backupFileName := fmt.Sprintf("%s.backup.%s", filepath.Base(output), timestamp)
		backupPath := filepath.Join(backupDir, backupFileName)

		fmt.Printf("\n⚠️  Файл %s уже существует.\n", output)
		fmt.Printf("Будет создан бэкап: %s\n", backupPath)

		if added, removed, diffErr := c.ContextDiff(output); diffErr != nil {
			pterm.FgYellow.Printf("⚠ Не удалось посчитать разницу контекстов: %v\n", diffErr)
		} else {
			fmt.Printf("added: %d", len(added))
			if len(added) > 0 {
				fmt.Printf(", %s", strings.Join(added, ", "))
			}
			fmt.Println()
			fmt.Printf("removed: %d", len(removed))
			if len(removed) > 0 {
				fmt.Printf(", %s", strings.Join(removed, ", "))
			}
			fmt.Println()
		}

		fmt.Print("Перезаписать? [y/N]: ")

		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("ошибка чтения ввода: %w", err)
		}
		response = strings.ToLower(strings.TrimSpace(response))

		if response != "y" && response != "yes" && response != "д" && response != "да" {
			fmt.Println("Отменено.")
			return nil
		}

		// Создаем бэкап
		if err := copyFile(output, backupPath); err != nil {
			return fmt.Errorf("не удалось создать бэкап: %w", err)
		}

		pterm.FgYellow.Printf("📦 Создан бэкап: %s\n", backupPath)
	}

	// Шаг 4: Запись результата
	if err := c.WriteOutput(output); err != nil {
		return fmt.Errorf("не удалось записать результат: %w", err)
	}

	pterm.FgGreen.Printf("✓ Готово: %s\n", output)

	// Шаг 5: Очистка исходных файлов (если запрошено)
	if cleanupAfter {
		if err := cleanupSourceFiles(c, output); err != nil {
			return fmt.Errorf("не удалось выполнить очистку: %w", err)
		}
	} else if files == "" {
		// Если не указан флаг --cleanup, спрашиваем пользователя
		fmt.Print("\nПереместить объединённые файлы в trash? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("ошибка чтения ввода: %w", err)
		}
		response = strings.ToLower(strings.TrimSpace(response))

		if response == "y" || response == "yes" || response == "д" || response == "да" {
			if err := cleanupSourceFiles(c, output); err != nil {
				return fmt.Errorf("не удалось выполнить очистку: %w", err)
			}
		}
	}

	return nil
}

// cleanupSourceFiles перемещает исходные файлы в trash
func cleanupSourceFiles(c *composer.Composer, outputPath string) error {
	loadedFiles := c.GetLoadedFiles()
	if len(loadedFiles) == 0 {
		return nil
	}

	// Создаём директорию trash
	trashDir := filepath.Join(os.Getenv("HOME"), ".kubeconfig-composer", "trash")
	if err := os.MkdirAll(trashDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать директорию trash: %w", err)
	}

	// Получаем абсолютный путь к выходному файлу
	absOutput, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("не удалось получить абсолютный путь: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405")
	movedCount := 0
	skippedCount := 0

	fmt.Println("\nПеремещение файлов в trash...")

	for _, f := range loadedFiles {
		sourcePath := f.GetFullPath()
		absSource, err := filepath.Abs(sourcePath)
		if err != nil {
			pterm.FgYellow.Printf("⚠ Пропущен %s: %v\n", sourcePath, err)
			skippedCount++
			continue
		}

		// Не перемещаем выходной файл
		if absSource == absOutput {
			skippedCount++
			continue
		}

		// Формируем имя файла в trash с timestamp
		baseName := filepath.Base(sourcePath)
		trashPath := filepath.Join(trashDir, fmt.Sprintf("%s.%s", timestamp, baseName))

		// Перемещаем файл
		if err := os.Rename(sourcePath, trashPath); err != nil {
			pterm.FgRed.Printf("✗ Не удалось переместить %s: %v\n", sourcePath, err)
			skippedCount++
			continue
		}

		pterm.FgGreen.Printf("✓ Перемещён: %s → trash/\n", filepath.Base(sourcePath))
		movedCount++
	}

	if movedCount > 0 {
		fmt.Printf("\n%d файлов перемещено в %s\n", movedCount, trashDir)
	}
	if skippedCount > 0 {
		fmt.Printf("%d файлов пропущено\n", skippedCount)
	}

	return nil
}

// copyFile копирует файл из src в dst
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}
