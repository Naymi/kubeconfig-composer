package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/naymi/kubeconfig-composer/pkg/scanner"
	"github.com/spf13/cobra"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

var listDir string

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "Показать список найденных kubeconfig файлов",
	Long:  `Сканирует директорию и показывает все найденные kubeconfig файлы с их контекстами.`,
	Example: `  # Показать файлы в ~/.kube
  kubeconfig-composer list

  # Показать файлы в другой директории
  kubeconfig-composer list --dir /path/to/configs`,
	RunE: runList,
}

func init() {
	rootCmd.AddCommand(listCmd)

	listCmd.Flags().StringVarP(&listDir, "dir", "d", filepath.Join(os.Getenv("HOME"), ".kube"), "Директория для сканирования")
}

func runList(cmd *cobra.Command, args []string) error {
	var files []string

	err := filepath.Walk(listDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		if !scanner.IsKubeconfigFile(path, info) {
			return nil
		}

		files = append(files, path)
		return nil
	})

	if err != nil {
		return fmt.Errorf("ошибка сканирования: %w", err)
	}

	if len(files) == 0 {
		fmt.Printf("Kubeconfig файлы не найдены в %s\n", listDir)
		return nil
	}

	fmt.Printf("Найдено %d kubeconfig файл(ов):\n\n", len(files))

	for _, file := range files {
		config, err := clientcmd.LoadFromFile(file)
		if err != nil {
			fmt.Printf("  ✗ %s (ошибка загрузки)\n", file)
			continue
		}

		fmt.Print("  ✓ ")
		// Выделяем имя файла цветом
		baseName := filepath.Base(file)
		dirPath := filepath.Dir(file)
		fmt.Printf("%s/", dirPath)
		fmt.Printf("\033[36m%s\033[0m\n", baseName) // Cyan color

		if len(config.Contexts) > 0 {
			contextNames := getContextNames(config.Contexts)
			fmt.Print("    Контексты: ")
			for i, name := range contextNames {
				if i > 0 {
					fmt.Print(", ")
				}
				fmt.Printf("\033[33m%s\033[0m", name) // Yellow color
			}
			fmt.Println()
		}
	}

	return nil
}

func getContextNames(contexts map[string]*clientcmdapi.Context) []string {
	names := make([]string, 0, len(contexts))
	for name := range contexts {
		names = append(names, name)
	}
	return names
}
