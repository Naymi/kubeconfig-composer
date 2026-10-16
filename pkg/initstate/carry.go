package initstate

import (
	"errors"
	"io/fs"
	"sort"
	"strings"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Session — состояние прежнего kc.yaml, которое должно пережить пересборку:
// kubectl, kubectx и kubens пишут его прямо в этот файл.
type Session struct {
	CurrentContext string
	Namespaces     map[string]string // имя контекста -> namespace
	Names          map[string]bool   // имена кластеров, пользователей и контекстов
}

// ReadSession читает прежний kc.yaml. Если файла нет, возвращает (nil, nil).
func ReadSession(path string) (*Session, error) {
	cfg, err := clientcmd.LoadFromFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	s := &Session{
		CurrentContext: cfg.CurrentContext,
		Namespaces:     map[string]string{},
		Names:          map[string]bool{},
	}
	for name := range cfg.Clusters {
		s.Names[name] = true
	}
	for name := range cfg.AuthInfos {
		s.Names[name] = true
	}
	for name, ctx := range cfg.Contexts {
		s.Names[name] = true
		s.Namespaces[name] = ctx.Namespace
	}
	return s, nil
}

// ApplyCurrentContext возвращает прежний current-context, если такой контекст
// есть в cfg. Вызывать до Dedupe: он не схлопывает контекст из current-context.
func (s *Session) ApplyCurrentContext(cfg *clientcmdapi.Config) {
	if s.CurrentContext == "" {
		return
	}
	if _, ok := cfg.Contexts[s.CurrentContext]; ok {
		cfg.CurrentContext = s.CurrentContext
	}
}

// ApplyNamespaces переносит непустые прежние namespace на контексты, которые
// есть в cfg. Пустой прежний namespace значение из источника не затирает.
func (s *Session) ApplyNamespaces(cfg *clientcmdapi.Config) {
	for name, ns := range s.Namespaces {
		if ns == "" {
			continue
		}
		if ctx, ok := cfg.Contexts[name]; ok {
			ctx.Namespace = ns
		}
	}
}

// LostForeign возвращает отсортированные имена, которые были в прежнем файле,
// пропали в cfg и не начинаются с "<prefix>:" — то есть записи, добавленные не
// нами (например, `aws eks update-kubeconfig`). Пропавшие записи с префиксом —
// это удалённые источники, о них не сообщаем. При пустом prefix отличить свои
// записи нельзя, поэтому возвращаются все пропавшие.
func (s *Session) LostForeign(cfg *clientcmdapi.Config, prefix string) []string {
	var lost []string
	for name := range s.Names {
		if prefix != "" && strings.HasPrefix(name, prefix+":") {
			continue
		}
		_, inCluster := cfg.Clusters[name]
		_, inUser := cfg.AuthInfos[name]
		_, inContext := cfg.Contexts[name]
		if !inCluster && !inUser && !inContext {
			lost = append(lost, name)
		}
	}
	sort.Strings(lost)
	return lost
}
