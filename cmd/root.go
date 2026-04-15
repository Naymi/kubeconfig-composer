package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "kubeconfig-composer",
	Short: "Утилита для объединения kubeconfig файлов",
	Long: `Kubeconfig Composer - инструмент для объединения множественных
kubeconfig файлов с автоматическим разрешением конфликтов имён.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
