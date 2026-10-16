package initstate

import (
	"errors"
	"io/fs"
	"sort"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Session — состояние прежнего kc.yaml, которое должно пережить пересборку:
// kubectl, kubectx и kubens пишут его прямо в этот файл.
type Session struct {
	CurrentContext string
	Namespaces     map[string]string // имя контекста -> namespace
	Names          map[string]bool   // имена кластеров, пользователей и контекстов прежнего файла; LostForeign сверяет их с State.Names
}

// configNames возвращает отсортированные без повторов имена кластеров,
// пользователей и контекстов cfg — то, что сохраняется в State.Names.
func configNames(cfg *clientcmdapi.Config) []string {
	set := map[string]bool{}
	for name := range cfg.Clusters {
		set[name] = true
	}
	for name := range cfg.AuthInfos {
		set[name] = true
	}
	for name := range cfg.Contexts {
		set[name] = true
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// contextNamespaces возвращает namespace каждого контекста cfg — то, что
// сохраняется в State.SourceNamespaces.
func contextNamespaces(cfg *clientcmdapi.Config) map[string]string {
	ns := make(map[string]string, len(cfg.Contexts))
	for name, ctx := range cfg.Contexts {
		ns[name] = ctx.Namespace
	}
	return ns
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

// ApplyNamespaces возвращает namespace, выбранный пользователем прямо в
// прежнем файле (kubens, kubectl config set-context), на контексты, которые
// есть в cfg. prevSource — namespace контекстов в сборке из источников на
// прошлой пересборке (State.SourceNamespaces). Прежний namespace переносится,
// если он непустой и отличается от того, что тогда дал источник, или если
// контекста в prevSource нет (в том числе prevSource == nil — состояния нет).
// Если прежний namespace совпадает с прошлым значением источника, его никто не
// менял, и побеждает текущее значение из источника.
func (s *Session) ApplyNamespaces(cfg *clientcmdapi.Config, prevSource map[string]string) {
	for name, ns := range s.Namespaces {
		if ns == "" {
			continue
		}
		if src, ok := prevSource[name]; ok && src == ns {
			continue
		}
		if ctx, ok := cfg.Contexts[name]; ok {
			ctx.Namespace = ns
		}
	}
}

// LostForeign возвращает отсортированные имена, которые были в прежнем файле,
// пропали в cfg и не записаны init на прошлой сборке (owned — State.Names).
// Это записи, добавленные в файл не init: `aws eks update-kubeconfig`, `merge`,
// ручная правка — с префиксом или без. Пропавшие записи из owned — это
// удалённые источники, о них не сообщаем. Если состояния нет (owned пуст),
// чужими считаются все пропавшие: файл собирал не init.
func (s *Session) LostForeign(cfg *clientcmdapi.Config, owned map[string]bool) []string {
	var lost []string
	for name := range s.Names {
		if owned[name] {
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
