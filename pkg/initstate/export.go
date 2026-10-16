package initstate

import "strings"

// ShellExport возвращает строку для eval в bash/zsh. Путь берётся в одинарные
// кавычки, внутренние одинарные кавычки экранируются как '\” — иначе путь с
// пробелом или кавычкой сломал бы eval при старте терминала.
func ShellExport(path string) string {
	return "export KUBECONFIG='" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}
