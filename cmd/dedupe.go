package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/naymi/kubeconfig-composer/pkg/cleaner"
	"github.com/olekukonko/tablewriter"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	dedupeOutput string
	dedupeDryRun bool
	dedupeYes    bool
)

var dedupeCmd = &cobra.Command{
	Use:   "dedupe",
	Short: "Удалить дубликаты контекстов, кластеров и пользователей",
	Long: `Схлопывает дубликаты в kubeconfig файле.

Дубликаты определяются по содержимому, а не по имени: кластеры с одинаковыми
server/CA, пользователи с одинаковыми учётными данными и контексты, ведущие к
одному и тому же (кластер + пользователь + namespace), объединяются в один
элемент. Такие дубликаты обычно появляются после повторных запусков merge,
когда одно и то же попадает в результат под именами вида foo, foo-config, foo-1.

Каноническим остаётся самое короткое имя (при равной длине — по алфавиту);
имена, на которые ссылается current-context, сохраняются в приоритете.`,
	Example: `  # Дедуплицировать ~/.kube/config (или первый путь из $KUBECONFIG)
  kubeconfig-composer dedupe

  # Посмотреть, что будет удалено, ничего не меняя
  kubeconfig-composer dedupe --dry-run

  # Дедуплицировать конкретный файл без запроса подтверждения
  kubeconfig-composer dedupe -o ~/.kube/config -y`,
	RunE: runDedupe,
}

func init() {
	rootCmd.AddCommand(dedupeCmd)

	dedupeCmd.Flags().StringVarP(&dedupeOutput, "output", "o", defaultKubeconfigPath(), "Путь к kubeconfig файлу")
	dedupeCmd.Flags().BoolVar(&dedupeDryRun, "dry-run", false, "Показать изменения, не записывая файл")
	dedupeCmd.Flags().BoolVarP(&dedupeYes, "yes", "y", false, "Не запрашивать подтверждение перед записью")
}

// defaultKubeconfigPath возвращает целевой kubeconfig: первый путь из $KUBECONFIG,
// иначе ~/.kube/config.
func defaultKubeconfigPath() string {
	if kc := os.Getenv("KUBECONFIG"); kc != "" {
		if parts := filepath.SplitList(kc); len(parts) > 0 && parts[0] != "" {
			return parts[0]
		}
	}
	return filepath.Join(os.Getenv("HOME"), ".kube", "config")
}

func runDedupe(cmd *cobra.Command, args []string) error {
	if _, err := os.Stat(dedupeOutput); os.IsNotExist(err) {
		return fmt.Errorf("файл не найден: %s", dedupeOutput)
	}

	config, err := clientcmd.LoadFromFile(dedupeOutput)
	if err != nil {
		return fmt.Errorf("не удалось загрузить конфиг: %w", err)
	}

	deduped, report := cleaner.Dedupe(config)

	if !report.Changed() {
		pterm.FgGreen.Printf("✓ Дубликатов не найдено: %s\n", dedupeOutput)
		return nil
	}

	printDedupeReport(report)

	fmt.Printf("\nКонтекстов: %d → %d (удалено дубликатов: %d)\n",
		len(config.Contexts), len(deduped.Contexts), report.TotalRemovedContexts())

	if dedupeDryRun {
		fmt.Println("Режим dry-run: изменения не применены")
		return nil
	}

	if !dedupeYes {
		fmt.Printf("\nПрименить к %s? [y/N]: ", dedupeOutput)
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.ToLower(strings.TrimSpace(response))
		if response != "y" && response != "yes" && response != "д" && response != "да" {
			fmt.Println("Отменено.")
			return nil
		}
	}

	// Создаём бэкап перед записью (та же директория, что и у merge/revert).
	backupPath, err := backupKubeconfig(dedupeOutput)
	if err != nil {
		return fmt.Errorf("не удалось создать бэкап: %w", err)
	}
	pterm.FgYellow.Printf("📦 Создан бэкап: %s\n", backupPath)

	if err := cleaner.WriteConfig(dedupeOutput, deduped); err != nil {
		return fmt.Errorf("не удалось записать файл: %w", err)
	}

	pterm.FgGreen.Printf("✓ Готово: %s\n", dedupeOutput)
	return nil
}

// backupKubeconfig копирует файл в ~/.kubeconfig-composer/backups и возвращает путь бэкапа.
func backupKubeconfig(path string) (string, error) {
	backupDir := filepath.Join(os.Getenv("HOME"), ".kubeconfig-composer", "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return "", err
	}
	timestamp := time.Now().Format("20060102-150405")
	backupPath := filepath.Join(backupDir, fmt.Sprintf("%s.backup.%s", filepath.Base(path), timestamp))
	if err := copyFile(path, backupPath); err != nil {
		return "", err
	}
	return backupPath, nil
}

func printDedupeReport(report *cleaner.DedupeReport) {
	fmt.Println()
	pterm.FgGreen.Println("✓ Найдены дубликаты")
	fmt.Println()

	table := tablewriter.NewWriter(os.Stdout)
	table.SetBorder(true)
	table.SetRowLine(true)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetHeader([]string{"Тип", "Оставлено", "Удалено (дубликаты)"})
	table.SetHeaderColor(
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgGreenColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgYellowColor},
	)
	table.SetColumnColor(
		tablewriter.Colors{tablewriter.FgWhiteColor},
		tablewriter.Colors{tablewriter.FgGreenColor},
		tablewriter.Colors{tablewriter.FgYellowColor},
	)

	appendMerges := func(kind string, merges []cleaner.Merge) {
		for _, m := range merges {
			table.Append([]string{kind, m.Canonical, strings.Join(m.Removed, "\n")})
		}
	}
	appendMerges("context", report.ContextMerges)
	appendMerges("cluster", report.ClusterMerges)
	appendMerges("user", report.UserMerges)

	table.Render()

	if len(report.RemovedClusters) > 0 {
		pterm.FgYellow.Printf("Удалены неиспользуемые кластеры: %s\n", strings.Join(report.RemovedClusters, ", "))
	}
	if len(report.RemovedUsers) > 0 {
		pterm.FgYellow.Printf("Удалены неиспользуемые пользователи: %s\n", strings.Join(report.RemovedUsers, ", "))
	}
}
