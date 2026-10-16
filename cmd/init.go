package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/naymi/kubeconfig-composer/pkg/initstate"
	"github.com/spf13/cobra"
)

var (
	initDir     string
	initOutput  string
	initPrefix  string
	initMaxSize int
	initRefresh bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Собрать kubeconfig при старте shell и вывести export KUBECONFIG",
	Long: `Предназначена для .bashrc/.zshrc: собирает ~/.kubeconfig-composer/kc.yaml
из всех kubeconfig файлов в ~/.kube и печатает команду export KUBECONFIG=...
для eval.

Файлы не парсятся, пока не изменились: по умолчанию init только сверяет
отпечаток (путь, mtime, размер) источников с сохранённым. Если источники
изменились, kc.yaml пересобирается без вопросов. Если они суммарно больше
--max-size МБ, пересборка при старте shell пропускается с предупреждением в
stderr; запустите init --refresh вручную.

kc.yaml — производный файл: current-context и namespace в нём сохраняются при
пересборке, остальные правки перезаписываются. Не кладите --output внутрь
--dir: там его подхватит merge как источник, а merge --cleanup унесёт в trash.
Новые кластеры добавляйте в отдельные файлы-источники, например:
  alias eks-add='KUBECONFIG=~/.kube/aws.yaml aws eks update-kubeconfig'`,
	Example: `  # В ~/.bashrc или ~/.zshrc
  eval "$(kubeconfig-composer init)"

  # Принудительно пересобрать, порог размера не учитывается
  kubeconfig-composer init --refresh

  # Другая директория источников и другой выходной файл
  eval "$(kubeconfig-composer init --dir ~/work/kube --output ~/work/kc.yaml)"`,
	// Ошибку печатает Execute() один раз; usage в eval-сценарии только мешает.
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)

	initCmd.Flags().StringVarP(&initDir, "dir", "d", filepath.Join(os.Getenv("HOME"), ".kube"), "Директория с kubeconfig файлами")
	initCmd.Flags().StringVarP(&initOutput, "output", "o", filepath.Join(initStateDir(), "kc.yaml"), "Собираемый файл (лучше вне --dir)")
	initCmd.Flags().StringVar(&initPrefix, "prefix", "kc", `Префикс имён, как у merge (пустая строка отключает)`)
	initCmd.Flags().IntVar(&initMaxSize, "max-size", 5, "Порог суммарного размера источников в МБ, выше которого пересборка при старте пропускается (0 — без порога)")
	initCmd.Flags().BoolVar(&initRefresh, "refresh", false, "Пересобрать сейчас, порог размера не учитывается")
}

// initStateDir — каталог состояния ~/.kubeconfig-composer. В нём же лежит
// kc.yaml по умолчанию: вне ~/.kube его не подхватит merge.
func initStateDir() string {
	return filepath.Join(os.Getenv("HOME"), ".kubeconfig-composer")
}

func runInit(cmd *cobra.Command, args []string) error {
	res, err := initstate.Run(initstate.Options{
		Dir:       initDir,
		Output:    initOutput,
		Prefix:    initPrefix,
		MaxSizeMB: initMaxSize,
		Refresh:   initRefresh,
		Version:   version,
		StateDir:  initStateDir(),
		Log:       os.Stderr,
	})

	if res.ExportOK {
		// Оболочка не должна ломаться: ошибку показываем, но export отдаём.
		if err != nil {
			fmt.Fprintf(os.Stderr, "kubeconfig-composer init: %v\n", err)
		}
		fmt.Println(initstate.ShellExport(res.Output))
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("kubeconfig-composer init: %s не создан", res.Output)
}
