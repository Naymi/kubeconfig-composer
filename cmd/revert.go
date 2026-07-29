package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var revertCmd = &cobra.Command{
	Use:   "revert",
	Short: "Восстановить конфиг из бэкапа",
	Long: `Восстанавливает kubeconfig файл из последнего бэкапа,
созданного командой merge.`,
	Example: `  # Восстановить ~/.kube/config из последнего бэкапа
  kubeconfig-composer revert

  # Восстановить конкретный файл
  kubeconfig-composer revert --output ~/.kube/merged-config

  # Показать список доступных бэкапов
  kubeconfig-composer revert --list`,
	RunE: runRevert,
}

var (
	revertOutput string
	listBackups  bool
)

func init() {
	rootCmd.AddCommand(revertCmd)

	revertCmd.Flags().StringVarP(&revertOutput, "output", "o", filepath.Join(os.Getenv("HOME"), ".kube", "config"), "Путь к файлу для восстановления")
	revertCmd.Flags().BoolVarP(&listBackups, "list", "l", false, "Показать список доступных бэкапов")
}

func runRevert(cmd *cobra.Command, args []string) error {
	// Находим все бэкапы для указанного файла
	backups, err := findBackups(revertOutput)
	if err != nil {
		return fmt.Errorf("не удалось найти бэкапы: %w", err)
	}

	if len(backups) == 0 {
		pterm.FgYellow.Printf("⚠️  Бэкапы для %s не найдены\n", revertOutput)
		return nil
	}

	// Если запрошен список бэкапов
	if listBackups {
		fmt.Printf("Доступные бэкапы для %s:\n\n", revertOutput)
		for i, backup := range backups {
			info, _ := os.Stat(backup)
			fmt.Printf("  %d. %s (%s)\n", i+1, filepath.Base(backup), info.ModTime().Format("2006-01-02 15:04:05"))
		}
		return nil
	}

	// Проверяем, совпадает ли текущий файл с последним бэкапом
	currentData, err := os.ReadFile(revertOutput)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("не удалось прочитать текущий файл: %w", err)
	}

	var targetBackup string
	var targetIndex int

	// Ищем бэкап, который отличается от текущего состояния
	for i, backup := range backups {
		backupData, err := os.ReadFile(backup)
		if err != nil {
			continue
		}

		// Если содержимое отличается, это наш целевой бэкап
		if string(currentData) != string(backupData) {
			targetBackup = backup
			targetIndex = i
			break
		}
	}

	// Если не нашли отличающийся бэкап, значит все бэкапы идентичны текущему файлу
	if targetBackup == "" {
		if len(backups) > 0 {
			pterm.FgYellow.Printf("⚠️  Все бэкапы идентичны текущему состоянию файла\n")
		}
		return nil
	}

	backupInfo, _ := os.Stat(targetBackup)

	fmt.Printf("Найден бэкап: %s\n", filepath.Base(targetBackup))
	fmt.Printf("Создан: %s\n", backupInfo.ModTime().Format("2006-01-02 15:04:05"))
	if targetIndex > 0 {
		fmt.Printf("(пропущено %d идентичных бэкапов)\n", targetIndex)
	}
	fmt.Printf("\nВосстановить %s из этого бэкапа? [y/N]: ", revertOutput)

	var response string
	fmt.Scanln(&response)
	response = strings.ToLower(strings.TrimSpace(response))

	if response != "y" && response != "yes" && response != "д" && response != "да" {
		fmt.Println("Отменено.")
		return nil
	}

	// Восстанавливаем из бэкапа
	if err := copyFile(targetBackup, revertOutput); err != nil {
		return fmt.Errorf("не удалось восстановить из бэкапа: %w", err)
	}

	pterm.FgGreen.Printf("✓ Восстановлено: %s\n", revertOutput)
	return nil
}

// findBackups находит все бэкапы для указанного файла и возвращает их отсортированными по времени (новые первые)
func findBackups(targetFile string) ([]string, error) {
	// Ищем бэкапы в директории .kubeconfig-composer/backups
	backupDir := filepath.Join(os.Getenv("HOME"), ".kubeconfig-composer", "backups")
	base := filepath.Base(targetFile)

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	var backups []string
	prefix := base + ".backup."

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if strings.HasPrefix(name, prefix) {
			backups = append(backups, filepath.Join(backupDir, name))
		}
	}

	// Сортируем по времени модификации (новые первые)
	sort.Slice(backups, func(i, j int) bool {
		infoI, _ := os.Stat(backups[i])
		infoJ, _ := os.Stat(backups[j])
		return infoI.ModTime().After(infoJ.ModTime())
	})

	return backups, nil
}
