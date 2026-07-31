package cleaner

import (
	"reflect"
	"sort"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Merge описывает схлопывание группы дубликатов в один канонический элемент.
type Merge struct {
	Canonical string   // имя, которое осталось
	Removed   []string // имена дубликатов, которые были удалены
}

// DedupeReport содержит сведения о том, что было объединено при дедупликации.
type DedupeReport struct {
	ClusterMerges   []Merge
	UserMerges      []Merge
	ContextMerges   []Merge
	RemovedClusters []string // неиспользуемые кластеры, удалённые в конце
	RemovedUsers    []string // неиспользуемые пользователи, удалённые в конце
}

// Changed возвращает true, если дедупликация что-то изменила.
func (r *DedupeReport) Changed() bool {
	return len(r.ClusterMerges) > 0 || len(r.UserMerges) > 0 ||
		len(r.ContextMerges) > 0 || len(r.RemovedClusters) > 0 || len(r.RemovedUsers) > 0
}

// TotalRemovedContexts возвращает количество удалённых контекстов-дубликатов.
func (r *DedupeReport) TotalRemovedContexts() int {
	n := 0
	for _, m := range r.ContextMerges {
		n += len(m.Removed)
	}
	return n
}

// Dedupe схлопывает кластеры, пользователей и контексты с идентичным содержимым.
//
// Сравнение выполняется по содержимому, а не по имени: два кластера считаются
// одинаковыми, если совпадают их поля (server, CA data, TLS-настройки и т.д.),
// то же самое для пользователей и контекстов. Это именно тот случай, который
// порождает merge, когда одно и то же попадает в результат несколько раз под
// разными именами (foo, foo-config, foo-1, ...).
//
// Функция не мутирует исходный config, а возвращает новый.
func Dedupe(cfg *clientcmdapi.Config) (*clientcmdapi.Config, *DedupeReport) {
	report := &DedupeReport{}

	out := clientcmdapi.NewConfig()
	out.Preferences = cfg.Preferences
	out.Extensions = cfg.Extensions

	// Имена, на которые ссылается текущий контекст, имеют приоритет при выборе
	// канонического имени — так current-context не ломается.
	preferredCluster := map[string]bool{}
	preferredUser := map[string]bool{}
	if cur, ok := cfg.Contexts[cfg.CurrentContext]; ok {
		preferredCluster[cur.Cluster] = true
		preferredUser[cur.AuthInfo] = true
	}

	// --- Кластеры ---
	clusterMap := map[string]string{} // старое имя -> каноническое
	for _, group := range groupByContent(sortedKeys(cfg.Clusters), func(a, b string) bool {
		return clustersEqual(cfg.Clusters[a], cfg.Clusters[b])
	}) {
		canonical := pickCanonical(group, preferredCluster)
		out.Clusters[canonical] = cfg.Clusters[canonical]
		var removed []string
		for _, name := range group {
			clusterMap[name] = canonical
			if name != canonical {
				removed = append(removed, name)
			}
		}
		if len(removed) > 0 {
			report.ClusterMerges = append(report.ClusterMerges, Merge{Canonical: canonical, Removed: removed})
		}
	}

	// --- Пользователи ---
	userMap := map[string]string{}
	for _, group := range groupByContent(sortedKeys(cfg.AuthInfos), func(a, b string) bool {
		return usersEqual(cfg.AuthInfos[a], cfg.AuthInfos[b])
	}) {
		canonical := pickCanonical(group, preferredUser)
		out.AuthInfos[canonical] = cfg.AuthInfos[canonical]
		var removed []string
		for _, name := range group {
			userMap[name] = canonical
			if name != canonical {
				removed = append(removed, name)
			}
		}
		if len(removed) > 0 {
			report.UserMerges = append(report.UserMerges, Merge{Canonical: canonical, Removed: removed})
		}
	}

	// --- Контексты ---
	// Сначала переносим ссылки на кластеры/пользователей на канонические имена,
	// чтобы контексты, отличавшиеся только этими ссылками, стали идентичными.
	remapped := make(map[string]*clientcmdapi.Context, len(cfg.Contexts))
	for name, ctx := range cfg.Contexts {
		nc := ctx.DeepCopy()
		if m, ok := clusterMap[ctx.Cluster]; ok {
			nc.Cluster = m
		}
		if m, ok := userMap[ctx.AuthInfo]; ok {
			nc.AuthInfo = m
		}
		remapped[name] = nc
	}

	preferredContext := map[string]bool{cfg.CurrentContext: true}
	contextMap := map[string]string{}
	for _, group := range groupByContent(sortedKeys(remapped), func(a, b string) bool {
		return contextsSameTarget(remapped[a], remapped[b])
	}) {
		canonical := pickCanonical(group, preferredContext)
		ctx := remapped[canonical].DeepCopy()
		// Контексты-дубликаты могут отличаться только namespace; сохраняем
		// непустой namespace, чтобы не потерять его при схлопывании.
		ctx.Namespace = pickNamespace(group, remapped, canonical)
		out.Contexts[canonical] = ctx
		var removed []string
		for _, name := range group {
			contextMap[name] = canonical
			if name != canonical {
				removed = append(removed, name)
			}
		}
		if len(removed) > 0 {
			report.ContextMerges = append(report.ContextMerges, Merge{Canonical: canonical, Removed: removed})
		}
	}

	// Переносим current-context на каноническое имя.
	out.CurrentContext = cfg.CurrentContext
	if m, ok := contextMap[cfg.CurrentContext]; ok {
		out.CurrentContext = m
	}

	// Удаляем кластеры/пользователей, на которые больше никто не ссылается.
	usedClusters := map[string]bool{}
	usedUsers := map[string]bool{}
	for _, ctx := range out.Contexts {
		usedClusters[ctx.Cluster] = true
		usedUsers[ctx.AuthInfo] = true
	}
	for _, name := range sortedKeys(out.Clusters) {
		if !usedClusters[name] {
			delete(out.Clusters, name)
			report.RemovedClusters = append(report.RemovedClusters, name)
		}
	}
	for _, name := range sortedKeys(out.AuthInfos) {
		if !usedUsers[name] {
			delete(out.AuthInfos, name)
			report.RemovedUsers = append(report.RemovedUsers, name)
		}
	}

	return out, report
}

// groupByContent группирует имена так, что в одной группе оказываются имена с
// идентичным содержимым (по функции equal). Первое имя каждой группы —
// представитель, с которым сравниваются остальные. Имена перебираются в
// отсортированном порядке для детерминированного результата.
func groupByContent(names []string, equal func(a, b string) bool) [][]string {
	var groups [][]string
	for _, name := range names {
		placed := false
		for i := range groups {
			if equal(name, groups[i][0]) {
				groups[i] = append(groups[i], name)
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, []string{name})
		}
	}
	return groups
}

// pickCanonical выбирает каноническое имя группы: сначала предпочтительные
// (например, на которые ссылается current-context), затем самое короткое, при
// равной длине — лексикографически меньшее. Так «prod» побеждает «prod-config».
func pickCanonical(group []string, preferred map[string]bool) string {
	best := ""
	// Приоритет — предпочтительным именам.
	for _, name := range group {
		if preferred[name] && (best == "" || betterName(name, best)) {
			best = name
		}
	}
	if best != "" {
		return best
	}
	for _, name := range group {
		if best == "" || betterName(name, best) {
			best = name
		}
	}
	return best
}

// betterName возвращает true, если a предпочтительнее b как каноническое имя.
func betterName(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

// ClustersEqual сообщает, идентичны ли два кластера по содержимому
// (путь-источник LocationOfOrigin игнорируется). Экспортируется для merge,
// чтобы распознавать одинаковые записи и не плодить дубликаты.
func ClustersEqual(a, b *clientcmdapi.Cluster) bool { return clustersEqual(a, b) }

// UsersEqual сообщает, идентичны ли два пользователя по содержимому.
func UsersEqual(a, b *clientcmdapi.AuthInfo) bool { return usersEqual(a, b) }

func clustersEqual(a, b *clientcmdapi.Cluster) bool {
	if a == nil || b == nil {
		return a == b
	}
	x, y := a.DeepCopy(), b.DeepCopy()
	// LocationOfOrigin хранит путь к исходному файлу и не относится к содержимому.
	x.LocationOfOrigin, y.LocationOfOrigin = "", ""
	return reflect.DeepEqual(x, y)
}

func usersEqual(a, b *clientcmdapi.AuthInfo) bool {
	if a == nil || b == nil {
		return a == b
	}
	x, y := a.DeepCopy(), b.DeepCopy()
	x.LocationOfOrigin, y.LocationOfOrigin = "", ""
	return reflect.DeepEqual(x, y)
}

// contextsSameTarget считает контексты дубликатами, если они ведут к одному и
// тому же кластеру и пользователю. Namespace намеренно игнорируется — контексты,
// отличающиеся только им, схлопываются (namespace выбирается в pickNamespace).
func contextsSameTarget(a, b *clientcmdapi.Context) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Cluster == b.Cluster && a.AuthInfo == b.AuthInfo
}

// pickNamespace выбирает namespace для схлопнутого контекста: namespace
// канонического контекста, если он непустой, иначе первый непустой из группы
// (в отсортированном порядке), иначе пустой.
func pickNamespace(group []string, ctxs map[string]*clientcmdapi.Context, canonical string) string {
	if ns := ctxs[canonical].Namespace; ns != "" {
		return ns
	}
	for _, name := range group {
		if ns := ctxs[name].Namespace; ns != "" {
			return ns
		}
	}
	return ""
}

// sortedKeys возвращает отсортированные ключи map со строковым ключом.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
