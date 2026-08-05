package cleaner

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// buildConfig собирает config из троек (context, cluster, user, namespace) с
// заданными server/token, чтобы легко воспроизводить дубликаты.
func newConfig() *clientcmdapi.Config {
	return clientcmdapi.NewConfig()
}

func addEntry(cfg *clientcmdapi.Config, ctxName, clusterName, userName, server, token, namespace string) {
	cfg.Clusters[clusterName] = &clientcmdapi.Cluster{Server: server}
	cfg.AuthInfos[userName] = &clientcmdapi.AuthInfo{Token: token}
	cfg.Contexts[ctxName] = &clientcmdapi.Context{
		Cluster:   clusterName,
		AuthInfo:  userName,
		Namespace: namespace,
	}
}

func TestDedupeCollapsesIdenticalDuplicates(t *testing.T) {
	cfg := newConfig()
	// Три контекста, ведущие к одному и тому же кластеру/пользователю под
	// разными именами — типичный результат повторных merge.
	addEntry(cfg, "prod", "prod", "prod-user", "https://prod:6443", "tok", "")
	addEntry(cfg, "prod-config", "prod-config", "prod-user-config", "https://prod:6443", "tok", "")
	addEntry(cfg, "prod-1", "prod-1", "prod-user-1", "https://prod:6443", "tok", "")
	cfg.CurrentContext = "prod-config"

	out, report := Dedupe(cfg)

	if got := len(out.Contexts); got != 1 {
		t.Fatalf("ожидался 1 контекст, получено %d", got)
	}
	// current-context имеет приоритет при выборе канонического имени, чтобы не
	// сломать активный контекст пользователя.
	if _, ok := out.Contexts["prod-config"]; !ok {
		t.Errorf("каноническим должен остаться current-context 'prod-config', контексты: %v", keysOf(out.Contexts))
	}
	if len(out.Clusters) != 1 || len(out.AuthInfos) != 1 {
		t.Errorf("ожидалось по 1 кластеру и пользователю, получено %d/%d", len(out.Clusters), len(out.AuthInfos))
	}
	if report.TotalRemovedContexts() != 2 {
		t.Errorf("ожидалось 2 удалённых контекста, получено %d", report.TotalRemovedContexts())
	}
	if out.CurrentContext != "prod-config" {
		t.Errorf("current-context должен остаться 'prod-config', получено %q", out.CurrentContext)
	}
}

func TestDedupeCollapsesNamespaceOnlyDifference(t *testing.T) {
	cfg := newConfig()
	// Один и тот же кластер/пользователь; контексты отличаются только namespace
	// (один пустой) — должны схлопнуться, непустой namespace сохраняется.
	addEntry(cfg, "a", "c1", "u1", "https://x:6443", "tok", "team-a")
	addEntry(cfg, "b", "c2", "u2", "https://x:6443", "tok", "")

	out, report := Dedupe(cfg)

	if len(out.Contexts) != 1 {
		t.Fatalf("контексты, отличающиеся только namespace, должны схлопнуться, получено %d", len(out.Contexts))
	}
	if report.TotalRemovedContexts() != 1 {
		t.Errorf("ожидался 1 удалённый контекст, получено %d", report.TotalRemovedContexts())
	}
	// Каноническое имя — 'a' (короче/по алфавиту), namespace должен сохраниться.
	ctx, ok := out.Contexts["a"]
	if !ok {
		t.Fatalf("ожидался контекст 'a', контексты: %v", keysOf(out.Contexts))
	}
	if ctx.Namespace != "team-a" {
		t.Errorf("непустой namespace должен сохраниться, получено %q", ctx.Namespace)
	}
}

func TestDedupePreservesCurrentContextName(t *testing.T) {
	cfg := newConfig()
	// Даже если current-context не самое короткое имя, оно должно остаться.
	addEntry(cfg, "short", "c-short", "u-short", "https://y:6443", "tok", "")
	addEntry(cfg, "longer-name", "c-long", "u-long", "https://y:6443", "tok", "")
	cfg.CurrentContext = "longer-name"

	out, _ := Dedupe(cfg)

	if _, ok := out.Contexts["longer-name"]; !ok {
		t.Errorf("current-context 'longer-name' должен сохраниться, контексты: %v", keysOf(out.Contexts))
	}
	if out.CurrentContext != "longer-name" {
		t.Errorf("current-context должен остаться 'longer-name', получено %q", out.CurrentContext)
	}
}

func TestDedupeNoDuplicates(t *testing.T) {
	cfg := newConfig()
	addEntry(cfg, "a", "c1", "u1", "https://a:6443", "tok-a", "")
	addEntry(cfg, "b", "c2", "u2", "https://b:6443", "tok-b", "")

	out, report := Dedupe(cfg)

	if report.Changed() {
		t.Errorf("не ожидалось изменений, отчёт: %+v", report)
	}
	if len(out.Contexts) != 2 {
		t.Errorf("оба контекста должны сохраниться, получено %d", len(out.Contexts))
	}
}

func TestDedupeRemovesUnusedClustersAndUsers(t *testing.T) {
	cfg := newConfig()
	addEntry(cfg, "a", "c1", "u1", "https://z:6443", "tok", "")
	// Кластер и пользователь без единого контекста — «сироты».
	cfg.Clusters["orphan"] = &clientcmdapi.Cluster{Server: "https://orphan:6443"}
	cfg.AuthInfos["orphan-user"] = &clientcmdapi.AuthInfo{Token: "orphan-tok"}

	out, report := Dedupe(cfg)

	if _, ok := out.Clusters["orphan"]; ok {
		t.Errorf("неиспользуемый кластер должен быть удалён, осталось %v", keysOf(out.Clusters))
	}
	if _, ok := out.AuthInfos["orphan-user"]; ok {
		t.Errorf("неиспользуемый пользователь должен быть удалён, осталось %v", keysOf(out.AuthInfos))
	}
	if len(report.RemovedClusters) == 0 {
		t.Errorf("ожидалось сообщение об удалённом кластере")
	}
	if len(report.RemovedUsers) == 0 {
		t.Errorf("ожидалось сообщение об удалённом пользователе")
	}
}

func TestClustersUsersEqualIgnoreEmptyVsNilExtensions(t *testing.T) {
	// После чтения из файла (clientcmd.LoadFromFile) Extensions становится
	// пустой (не nil) картой, даже если в файле их не было. Кластер/пользователь
	// без Extensions вообще (nil) и такой же, но с пустой картой, должны
	// считаться идентичными — иначе только что загруженный с диска объект и
	// собранный в памяти отличались бы без реальной причины в содержимом.
	clusterNil := &clientcmdapi.Cluster{Server: "https://x"}
	clusterEmpty := &clientcmdapi.Cluster{Server: "https://x", Extensions: map[string]runtime.Object{}}
	if !ClustersEqual(clusterNil, clusterEmpty) {
		t.Error("кластеры с nil и пустой (не nil) картой Extensions должны считаться идентичными")
	}

	userNil := &clientcmdapi.AuthInfo{Token: "t"}
	userEmpty := &clientcmdapi.AuthInfo{Token: "t", Extensions: map[string]runtime.Object{}}
	if !UsersEqual(userNil, userEmpty) {
		t.Error("пользователи с nil и пустой (не nil) картой Extensions должны считаться идентичными")
	}
}

func keysOf[V any](m map[string]V) []string {
	return sortedKeys(m)
}
