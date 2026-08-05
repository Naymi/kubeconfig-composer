package composer

import (
	"fmt"
	"strings"
)

// genericNames — типовые/бессмысленные имена, которые kubeadm и большинство
// облачных провайдеров подставляют по умолчанию. Сталкиваясь с таким именем,
// merge заменяет его путём к файлу-источнику, чтобы результат был осмысленным.
var genericNames = map[string]bool{
	"default":          true,
	"kubernetes":       true,
	"kubernetes-admin": true,
	"cluster.local":    true,
	// Часто встречающиеся плейсхолдер-имена (k3s, rke, самописные кластеры и
	// т.п.) — сами по себе не несут смысла, годятся только как заглушки.
	"local":   true,
	"user":    true,
	"admin":   true,
	"cluster": true,
	"context": true,
}

// isGenericName сообщает, является ли имя типовым (default, kubernetes-admin,
// cluster.local, а также составные вида "user@cluster", где хотя бы одна
// сторона — типовое имя, например "kubernetes-admin@cluster.local").
func isGenericName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if genericNames[lower] {
		return true
	}
	if user, cluster, ok := strings.Cut(lower, "@"); ok {
		return genericNames[user] || genericNames[cluster]
	}
	return false
}

// numberedName формирует резервное имя при повторном конфликте: "_N" для
// имён, подставленных из пути (useNumericSuffix), иначе привычное "-N".
func numberedName(name string, n int, useNumericSuffix bool) string {
	if useNumericSuffix {
		return fmt.Sprintf("%s_%d", name, n)
	}
	return fmt.Sprintf("%s-%d", name, n)
}
