package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/naymi/kubeconfig-composer/pkg/composer"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var (
	kubeDir string
	files   string
	output  string
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
  kubeconfig-composer merge --output ~/.kube/merged-config`,
	RunE: runMerge,
}

func init() {
	rootCmd.AddCommand(mergeCmd)

	mergeCmd.Flags().StringVarP(&kubeDir, "dir", "d", filepath.Join(os.Getenv("HOME"), ".kube"), "Директория для сканирования kubeconfig файлов")
	mergeCmd.Flags().StringVarP(&files, "files", "f", "", "Список kubeconfig файлов через запятую")
	mergeCmd.Flags().StringVarP(&output, "output", "o", "merged-kubeconfig.yaml", "Путь к выходному файлу")
}

func runMerge(cmd *cobra.Command, args []string) error {
	c := composer.New()

	// Шаг 1: Загрузка файлов
	if files != "" {
		filePaths := strings.Split(files, ",")
		for i, path := range filePaths {
			filePaths[i] = strings.TrimSpace(path)
		}
		fmt.Printf("Загрузка %d файлов...\n", len(filePaths))
		if err := c.LoadFiles(filePaths); err != nil {
			return fmt.Errorf("не удалось загрузить файлы: %w", err)
		}
	} else {
		fmt.Printf("Сканирование %s...\n", kubeDir)
		if err := c.ScanDirectory(kubeDir); err != nil {
			return fmt.Errorf("не удалось просканировать директорию: %w", err)
		}
		fmt.Printf("Найдено %d файлов\n", len(c.GetLoadedFiles()))
	}

	// Шаг 2: Объединение конфигураций
	fmt.Println("Объединение...")
	if err := c.Merge(); err != nil {
		return fmt.Errorf("не удалось объединить конфигурации: %w", err)
	}

	// Шаг 3: Запись результата
	if err := c.WriteOutput(output); err != nil {
		return fmt.Errorf("не удалось записать результат: %w", err)
	}

	pterm.FgGreen.Printf("✓ Готово: %s\n", output)
	return nil
}
