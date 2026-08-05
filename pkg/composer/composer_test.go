package composer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/naymi/kubeconfig-composer/pkg/scanner"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestGetUniqueName(t *testing.T) {
	c := New(true) // autoAccept = true для тестов

	name1 := c.getUniqueName("prod", "context", "source1", "/path/to/file1", "prod", false)
	if name1 != "prod" {
		t.Errorf("Expected 'prod', got '%s'", name1)
	}

	name2 := c.getUniqueName("prod", "context", "source2", "/path/to/file2", "prod", false)
	if name2 == "prod" {
		t.Errorf("Expected unique name, got '%s'", name2)
	}

	name3 := c.getUniqueName("prod", "context", "source3", "/path/to/file3", "prod", false)
	if name3 == "prod" || name3 == name2 {
		t.Errorf("Expected unique name, got '%s'", name3)
	}
}

func TestMergeConfig(t *testing.T) {
	c := New(true) // autoAccept = true для тестов

	config1 := clientcmdapi.NewConfig()
	config1.Clusters["cluster1"] = &clientcmdapi.Cluster{Server: "https://server1"}
	config1.AuthInfos["user1"] = &clientcmdapi.AuthInfo{Token: "token1"}
	config1.Contexts["context1"] = &clientcmdapi.Context{
		Cluster:  "cluster1",
		AuthInfo: "user1",
	}

	config2 := clientcmdapi.NewConfig()
	config2.Clusters["cluster1"] = &clientcmdapi.Cluster{Server: "https://server2"}
	config2.AuthInfos["user1"] = &clientcmdapi.AuthInfo{Token: "token2"}
	config2.Contexts["context1"] = &clientcmdapi.Context{
		Cluster:  "cluster1",
		AuthInfo: "user1",
	}

	c.configs = []configWithSource{
		{config: config1, source: "config1", fullPath: "/path/to/config1"},
		{config: config2, source: "config2", fullPath: "/path/to/config2"},
	}
	c.Merge()

	// Имя контекста всегда получает путь к файлу-источнику, так что контексты
	// из двух разных файлов не совпадают по имени и не нуждаются в отдельном
	// разрешении конфликта.
	if len(c.mergedConfig.Contexts) != 2 {
		t.Errorf("Expected 2 contexts, got %d: %v", len(c.mergedConfig.Contexts), keysOf(c.mergedConfig.Contexts))
	}

	if _, exists := c.mergedConfig.Contexts["kc:context1:/path/to/config1"]; !exists {
		t.Errorf("Expected 'kc:context1:/path/to/config1' to exist, got %v", keysOf(c.mergedConfig.Contexts))
	}
	if _, exists := c.mergedConfig.Contexts["kc:context1:/path/to/config2"]; !exists {
		t.Errorf("Expected 'kc:context1:/path/to/config2' to exist, got %v", keysOf(c.mergedConfig.Contexts))
	}
}

// makeConfig собирает одиночный конфиг для тестов идемпотентности.
func makeConfig(ctx, cluster, user, server, token string) *clientcmdapi.Config {
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[cluster] = &clientcmdapi.Cluster{Server: server}
	cfg.AuthInfos[user] = &clientcmdapi.AuthInfo{Token: token}
	cfg.Contexts[ctx] = &clientcmdapi.Context{Cluster: cluster, AuthInfo: user}
	return cfg
}

func TestMergeReusesIdenticalEntries(t *testing.T) {
	// Два входа с идентичным содержимым под одинаковыми именами (например,
	// существующий конфиг + тот же исходник). Имя каждой сущности всегда
	// получает путь файла в скобках (разный для "a" и "b"), поэтому прямого
	// переиспользования по ключу при merge не происходит — дубликаты
	// схлопываются на шаге Dedupe, как и происходит в реальном пайплайне.
	c := New(true)
	c.configs = []configWithSource{
		{config: makeConfig("ctx", "c1", "u1", "https://x", "t"), source: "a", fullPath: "/a"},
		{config: makeConfig("ctx", "c1", "u1", "https://x", "t"), source: "b", fullPath: "/b"},
	}
	if err := c.Merge(); err != nil {
		t.Fatal(err)
	}
	c.Dedupe()
	if len(c.mergedConfig.Clusters) != 1 || len(c.mergedConfig.AuthInfos) != 1 || len(c.mergedConfig.Contexts) != 1 {
		t.Fatalf("идентичные записи должны схлопнуться после Dedupe: clusters=%d users=%d contexts=%d",
			len(c.mergedConfig.Clusters), len(c.mergedConfig.AuthInfos), len(c.mergedConfig.Contexts))
	}
}

func TestMergeDedupeCollapsesRenamedDuplicates(t *testing.T) {
	// Одинаковое содержимое под РАЗНЫМИ именами (как после прошлых merge)
	// должно схлопнуться на шаге Dedupe.
	c := New(true)
	c.configs = []configWithSource{
		{config: makeConfig("ctx", "c1", "u1", "https://x", "t"), source: "a", fullPath: "/a"},
		{config: makeConfig("ctx-config", "c1-config", "u1-config", "https://x", "t"), source: "b", fullPath: "/b"},
	}
	if err := c.Merge(); err != nil {
		t.Fatal(err)
	}
	c.Dedupe()
	if len(c.mergedConfig.Contexts) != 1 {
		t.Fatalf("контексты с одинаковой целью должны схлопнуться, got %d", len(c.mergedConfig.Contexts))
	}
	if len(c.mergedConfig.Clusters) != 1 || len(c.mergedConfig.AuthInfos) != 1 {
		t.Fatalf("идентичные кластеры/пользователи должны схлопнуться: clusters=%d users=%d",
			len(c.mergedConfig.Clusters), len(c.mergedConfig.AuthInfos))
	}
}

func TestMergeKeepsGenuineConflicts(t *testing.T) {
	// Одно имя, но РАЗНОЕ содержимое — это настоящий конфликт, записи должны
	// сохраниться раздельно.
	c := New(true)
	c.configs = []configWithSource{
		{config: makeConfig("ctx", "c1", "u1", "https://x", "t1"), source: "a", fullPath: "/a"},
		{config: makeConfig("ctx", "c1", "u1", "https://y", "t2"), source: "b", fullPath: "/b"},
	}
	if err := c.Merge(); err != nil {
		t.Fatal(err)
	}
	c.Dedupe()
	if len(c.mergedConfig.Clusters) != 2 {
		t.Fatalf("разные кластеры под одним именем должны остаться раздельными, got %d", len(c.mergedConfig.Clusters))
	}
}

func TestConflictPreviewMatchesActualNewName(t *testing.T) {
	// Таблица "ДО/ПОСЛЕ" (showConflictsAndAsk/ShowAppliedChanges) не должна
	// показывать устаревший формат "оригинальноеИмя-source" — с тех пор как
	// имя всегда строится как "<prefix>:<имя>:<путь>", реальное имя при
	// конфликте получает числовой суффикс "_N" (см. getUniqueName), и
	// предпросмотр обязан ему соответствовать.
	c := New(true)
	c.SetBaseDir("/dir")
	cfg1 := makeConfig("ctx", "prod", "u", "https://a", "t1")
	cfg2 := makeConfig("ctx", "prod", "u", "https://b", "t2")
	c.configs = []configWithSource{
		{config: cfg1, source: "config", fullPath: "/dir/config.yaml"},
		{config: cfg2, source: "config", fullPath: "/dir/config.yaml"},
	}
	if err := c.Merge(); err != nil {
		t.Fatal(err)
	}

	if len(c.conflicts) != 1 {
		t.Fatalf("ожидался 1 зафиксированный конфликт, получено %d: %+v", len(c.conflicts), c.conflicts)
	}
	const want = "kc:prod:config.yaml_1"
	if got := c.conflicts[0].clusterNew; got != want {
		t.Errorf("предпросмотр конфликта должен показывать реальное новое имя %q, получено %q", want, got)
	}
	if _, ok := c.mergedConfig.Clusters[want]; !ok {
		t.Errorf("фактическое имя после разрешения конфликта должно совпадать с предпросмотром %q, получено %v", want, keysOf(c.mergedConfig.Clusters))
	}
}

func TestContextsSameContentIgnoresNameDrift(t *testing.T) {
	// Прямая проверка contextsSameContent: если кластер/пользователь, на
	// которые ссылается контекст, названы по-разному, но содержимое (сервер,
	// токен) идентично — контексты считаются одинаковыми.
	clusters := map[string]*clientcmdapi.Cluster{
		"cluster-a": {Server: "https://x"},
		"cluster-b": {Server: "https://x"},
		"cluster-c": {Server: "https://y"},
	}
	users := map[string]*clientcmdapi.AuthInfo{
		"user-a": {Token: "t"},
		"user-b": {Token: "t"},
	}
	a := &clientcmdapi.Context{Cluster: "cluster-a", AuthInfo: "user-a"}
	bSameContent := &clientcmdapi.Context{Cluster: "cluster-b", AuthInfo: "user-b"}
	cDifferentContent := &clientcmdapi.Context{Cluster: "cluster-c", AuthInfo: "user-a"}

	if !contextsSameContent(a, bSameContent, clusters, users) {
		t.Error("контексты с разными именами кластера/пользователя, но идентичным содержимым, должны считаться одинаковыми")
	}
	if contextsSameContent(a, cDifferentContent, clusters, users) {
		t.Error("контексты с разным содержимым кластера не должны считаться одинаковыми")
	}
}

func TestMergeIgnoresClusterNameDriftWithSameContent(t *testing.T) {
	// Сквозной сценарий: контекст с уже финализированным именем (несёт префикс
	// "kc:") встречается дважды, но во втором случае его кластер/пользователь
	// оказались названы иначе (дрейф имени — например, пересчитаны под другой
	// путь), хотя сервер/токен не изменились. Это не должно считаться
	// конфликтом и не должно плодить вторую копию контекста.
	base := clientcmdapi.NewConfig()
	base.Clusters["kc:prod:base"] = &clientcmdapi.Cluster{Server: "https://x"}
	base.AuthInfos["kc:user:base"] = &clientcmdapi.AuthInfo{Token: "t"}
	base.Contexts["kc:ctx:base"] = &clientcmdapi.Context{Cluster: "kc:prod:base", AuthInfo: "kc:user:base"}

	drifted := clientcmdapi.NewConfig()
	drifted.Clusters["kc:prod:drifted"] = &clientcmdapi.Cluster{Server: "https://x"} // тот же сервер
	drifted.AuthInfos["kc:user:drifted"] = &clientcmdapi.AuthInfo{Token: "t"}        // тот же токен
	drifted.Contexts["kc:ctx:base"] = &clientcmdapi.Context{Cluster: "kc:prod:drifted", AuthInfo: "kc:user:drifted"}

	c := New(true)
	c.configs = []configWithSource{
		{config: base, source: "base", fullPath: "/dir/base.yaml"},
		{config: drifted, source: "drifted", fullPath: "/dir/drifted.yaml"},
	}
	if err := c.Merge(); err != nil {
		t.Fatal(err)
	}

	if len(c.conflicts) != 0 {
		t.Errorf("дрейф имени кластера/пользователя при идентичном содержимом не должен считаться конфликтом, получено %+v", c.conflicts)
	}
	if len(c.mergedConfig.Contexts) != 1 {
		t.Errorf("контекст с одинаковым (по содержимому) кластером/пользователем не должен дублироваться, получено %v", keysOf(c.mergedConfig.Contexts))
	}
}

func TestMergeReplacesGenericNamesWithSourcePath(t *testing.T) {
	// kubeadm-style типовые имена (cluster.local / kubernetes-admin /
	// kubernetes-admin@cluster.local) не несут смысла — вместо них должно
	// использоваться имя файла-источника без расширения (source), а путь к
	// нему относительно --dir добавляется как обычно.
	c := New(true)
	c.SetBaseDir("/dir")
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["cluster.local"] = &clientcmdapi.Cluster{Server: "https://x"}
	cfg.AuthInfos["kubernetes-admin"] = &clientcmdapi.AuthInfo{Token: "t"}
	cfg.Contexts["kubernetes-admin@cluster.local"] = &clientcmdapi.Context{
		Cluster: "cluster.local", AuthInfo: "kubernetes-admin",
	}
	c.configs = []configWithSource{
		{config: cfg, source: "config", fullPath: "/dir/nested/config.yaml"},
	}
	if err := c.Merge(); err != nil {
		t.Fatal(err)
	}

	const want = "kc:config:nested/config.yaml"
	if _, ok := c.mergedConfig.Clusters[want]; !ok {
		t.Errorf("ожидалось имя кластера %q вместо типового, получено %v", want, keysOf(c.mergedConfig.Clusters))
	}
	if _, ok := c.mergedConfig.AuthInfos[want]; !ok {
		t.Errorf("ожидалось имя пользователя %q вместо типового, получено %v", want, keysOf(c.mergedConfig.AuthInfos))
	}
	if _, ok := c.mergedConfig.Contexts[want]; !ok {
		t.Errorf("ожидалось имя контекста %q вместо типового, получено %v", want, keysOf(c.mergedConfig.Contexts))
	}
}

// keysOf собирает ключи карты в срез (для сообщений об ошибках в тестах).
func keysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestMergeGenericNameCollisionWithDifferentContentGetsNumericSuffix(t *testing.T) {
	// Два разных файла оба используют типовое имя кластера, но с разным
	// содержимым под одинаковым путём-производным именем (имитируем это
	// напрямую, задав два конфига с одинаковым fullPath, что на практике
	// соответствует ситуации "тот же путь, но файл успел измениться").
	c := New(true)
	c.SetBaseDir("/dir")
	cfg1 := clientcmdapi.NewConfig()
	cfg1.Clusters["cluster.local"] = &clientcmdapi.Cluster{Server: "https://a"}
	cfg2 := clientcmdapi.NewConfig()
	cfg2.Clusters["cluster.local"] = &clientcmdapi.Cluster{Server: "https://b"}
	c.configs = []configWithSource{
		{config: cfg1, source: "config", fullPath: "/dir/config.yaml"},
		{config: cfg2, source: "config", fullPath: "/dir/config.yaml"},
	}
	if err := c.Merge(); err != nil {
		t.Fatal(err)
	}

	if _, ok := c.mergedConfig.Clusters["kc:config:config.yaml"]; !ok {
		t.Errorf("ожидалось базовое имя 'kc:config:config.yaml', получено %v", keysOf(c.mergedConfig.Clusters))
	}
	if _, ok := c.mergedConfig.Clusters["kc:config:config.yaml_1"]; !ok {
		t.Errorf("ожидался числовой суффикс 'kc:config:config.yaml_1' при конфликте типовых имён, получено %v", keysOf(c.mergedConfig.Clusters))
	}
}

func TestMergeAlwaysAppendsPathToEntityNames(t *testing.T) {
	// Путь к файлу-источнику добавляется ко ВСЕМ сущностям — кластерам,
	// пользователям и контекстам — даже если у них осмысленное имя, а не
	// только к типовым/конфликтующим. Формат: "<prefix>:<имя>:<путь>".
	c := New(true)
	c.SetBaseDir("/dir")
	c.configs = []configWithSource{
		{config: makeConfig("my-prod-context", "my-cluster", "my-user", "https://x", "t"), source: "config", fullPath: "/dir/nested/config.yaml"},
	}
	if err := c.Merge(); err != nil {
		t.Fatal(err)
	}

	if _, ok := c.mergedConfig.Clusters["kc:my-cluster:nested/config.yaml"]; !ok {
		t.Errorf("ожидалось имя кластера 'kc:my-cluster:nested/config.yaml', получено %v", keysOf(c.mergedConfig.Clusters))
	}
	if _, ok := c.mergedConfig.AuthInfos["kc:my-user:nested/config.yaml"]; !ok {
		t.Errorf("ожидалось имя пользователя 'kc:my-user:nested/config.yaml', получено %v", keysOf(c.mergedConfig.AuthInfos))
	}
	if _, ok := c.mergedConfig.Contexts["kc:my-prod-context:nested/config.yaml"]; !ok {
		t.Errorf("ожидалось имя контекста 'kc:my-prod-context:nested/config.yaml', получено %v", keysOf(c.mergedConfig.Contexts))
	}
}

func TestMergeCustomNamePrefix(t *testing.T) {
	// Префикс настраивается через SetNamePrefix (флаг --prefix в CLI, без
	// двоеточия — оно добавляется автоматически); пустая строка отключает его.
	c := New(true)
	c.SetBaseDir("/dir")
	c.SetNamePrefix("src")
	c.configs = []configWithSource{
		{config: makeConfig("ctx", "cluster.local", "u1", "https://x", "t"), source: "config", fullPath: "/dir/config.yaml"},
	}
	if err := c.Merge(); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.mergedConfig.Clusters["src:config:config.yaml"]; !ok {
		t.Errorf("ожидалось имя кластера с кастомным префиксом 'src:config:config.yaml', получено %v", keysOf(c.mergedConfig.Clusters))
	}
	if _, ok := c.mergedConfig.Contexts["src:ctx:config.yaml"]; !ok {
		t.Errorf("ожидалось имя контекста с кастомным префиксом 'src:ctx:config.yaml', получено %v", keysOf(c.mergedConfig.Contexts))
	}
}

func TestMergeEmptyPrefixOmitsLeadingColon(t *testing.T) {
	// Пустой префикс отключается полностью — не должно оставаться "лишнего"
	// ведущего двоеточия перед именем.
	c := New(true)
	c.SetBaseDir("/dir")
	c.SetNamePrefix("")
	c.configs = []configWithSource{
		{config: makeConfig("ctx", "my-cluster", "u1", "https://x", "t"), source: "config", fullPath: "/dir/config.yaml"},
	}
	if err := c.Merge(); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.mergedConfig.Clusters["my-cluster:config.yaml"]; !ok {
		t.Errorf("ожидалось имя кластера без префикса 'my-cluster:config.yaml', получено %v", keysOf(c.mergedConfig.Clusters))
	}
}

func TestMergeDoesNotReApplyPrefixOnReRun(t *testing.T) {
	// Симулируем повторный merge: сущности уже были приведены к формату
	// "<prefix>:<имя>:<путь>" в прошлом запуске. При перезагрузке базового
	// (output) файла путь текущего запуска (fullPath самого output-файла) не
	// должен наслаиваться поверх уже финализированного имени — идемпотентность
	// определяется по тому, что имя уже начинается с "kc:", без Extensions.
	// Работает одинаково и для типового (кластер/пользователь), и для
	// осмысленного (контекст) исходного имени.
	base := makeConfig("kc:my-prod-context:nested/config.yaml", "kc:satellite03:satellite03.conf", "kc:satellite03:satellite03.conf", "https://x", "t")

	c := New(true)
	c.SetBaseDir("/dir")
	c.configs = []configWithSource{
		{config: base, source: "config", fullPath: "/dir/config"},
	}
	if err := c.Merge(); err != nil {
		t.Fatal(err)
	}

	if _, ok := c.mergedConfig.Clusters["kc:satellite03:satellite03.conf"]; !ok {
		t.Errorf("имя уже финализированного кластера не должно меняться повторно, получено %v", keysOf(c.mergedConfig.Clusters))
	}
	if _, ok := c.mergedConfig.Contexts["kc:my-prod-context:nested/config.yaml"]; !ok {
		t.Errorf("имя уже финализированного контекста не должно меняться повторно, получено %v", keysOf(c.mergedConfig.Contexts))
	}
}

func TestMergeCarriesOverCurrentContext(t *testing.T) {
	// current-context из исходного файла должен перейти в итоговый конфиг под
	// финальным (возможно переименованным) именем контекста — иначе OutputUnchanged
	// всегда считал бы результат "изменившимся" по сравнению с реальным
	// ~/.kube/config, у которого current-context почти всегда задан.
	c := New(true)
	cfg := makeConfig("my-prod-context", "my-cluster", "my-user", "https://x", "t")
	cfg.CurrentContext = "my-prod-context"
	c.configs = []configWithSource{
		{config: cfg, source: "config", fullPath: "/dir/config.yaml"},
	}
	if err := c.Merge(); err != nil {
		t.Fatal(err)
	}

	if c.mergedConfig.CurrentContext == "" {
		t.Fatal("current-context не должен быть пустым после merge")
	}
	if _, ok := c.mergedConfig.Contexts[c.mergedConfig.CurrentContext]; !ok {
		t.Errorf("current-context %q должен указывать на существующий контекст, доступны %v",
			c.mergedConfig.CurrentContext, keysOf(c.mergedConfig.Contexts))
	}
}

func TestMergeFirstSourceCurrentContextWins(t *testing.T) {
	// Если current-context задан в нескольких исходных файлах, побеждает
	// первый по порядку обработки (обычно это уже существующий output,
	// загружаемый как база) — последующие файлы его не перезаписывают.
	c := New(true)
	base := makeConfig("base-ctx", "c1", "u1", "https://a", "t1")
	base.CurrentContext = "base-ctx"
	other := makeConfig("other-ctx", "c2", "u2", "https://b", "t2")
	other.CurrentContext = "other-ctx"
	c.configs = []configWithSource{
		{config: base, source: "base", fullPath: "/dir/base.yaml"},
		{config: other, source: "other", fullPath: "/dir/other.yaml"},
	}
	if err := c.Merge(); err != nil {
		t.Fatal(err)
	}

	want := "kc:base-ctx:/dir/base.yaml"
	if c.mergedConfig.CurrentContext != want {
		t.Errorf("ожидался current-context от первого источника %q, получено %q", want, c.mergedConfig.CurrentContext)
	}
}

func TestOutputUnchangedTrueWhenContentMatches(t *testing.T) {
	// Записываем на диск конфиг через clientcmd (как это делает cmd/merge.go),
	// затем строим Composer с идентичным по содержимому mergedConfig — при
	// перезагрузке с диска LocationOfOrigin у каждого объекта проставится в
	// путь файла, а не в исходный source; OutputUnchanged должен это игнорировать.
	dir := t.TempDir()
	path := filepath.Join(dir, "config")

	onDisk := clientcmdapi.NewConfig()
	onDisk.Clusters["kc:prod:a.yaml"] = &clientcmdapi.Cluster{Server: "https://x"}
	onDisk.AuthInfos["kc:prod:a.yaml"] = &clientcmdapi.AuthInfo{Token: "t"}
	onDisk.Contexts["kc:prod:a.yaml"] = &clientcmdapi.Context{Cluster: "kc:prod:a.yaml", AuthInfo: "kc:prod:a.yaml"}
	onDisk.CurrentContext = "kc:prod:a.yaml"
	if err := clientcmd.WriteToFile(*onDisk, path); err != nil {
		t.Fatal(err)
	}

	c := New(true)
	c.mergedConfig = clientcmdapi.NewConfig()
	c.mergedConfig.Clusters["kc:prod:a.yaml"] = &clientcmdapi.Cluster{Server: "https://x"}
	c.mergedConfig.AuthInfos["kc:prod:a.yaml"] = &clientcmdapi.AuthInfo{Token: "t"}
	c.mergedConfig.Contexts["kc:prod:a.yaml"] = &clientcmdapi.Context{Cluster: "kc:prod:a.yaml", AuthInfo: "kc:prod:a.yaml"}
	c.mergedConfig.CurrentContext = "kc:prod:a.yaml"

	unchanged, err := c.OutputUnchanged(path)
	if err != nil {
		t.Fatal(err)
	}
	if !unchanged {
		t.Error("ожидалось, что идентичный по содержимому результат считается неизменившимся")
	}
}

func TestOutputUnchangedFalseWhenContentDiffers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")

	onDisk := clientcmdapi.NewConfig()
	onDisk.Clusters["kc:prod:a.yaml"] = &clientcmdapi.Cluster{Server: "https://x"}
	if err := clientcmd.WriteToFile(*onDisk, path); err != nil {
		t.Fatal(err)
	}

	c := New(true)
	c.mergedConfig = clientcmdapi.NewConfig()
	c.mergedConfig.Clusters["kc:prod:a.yaml"] = &clientcmdapi.Cluster{Server: "https://CHANGED"}

	unchanged, err := c.OutputUnchanged(path)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged {
		t.Error("ожидалось, что изменившийся сервер считается изменением")
	}
}

func TestContextDiffReportsAddedAndRemoved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")

	onDisk := clientcmdapi.NewConfig()
	onDisk.Contexts["kc:stays:a.yaml"] = &clientcmdapi.Context{Cluster: "c", AuthInfo: "u"}
	onDisk.Contexts["kc:gone:b.yaml"] = &clientcmdapi.Context{Cluster: "c", AuthInfo: "u"}
	if err := clientcmd.WriteToFile(*onDisk, path); err != nil {
		t.Fatal(err)
	}

	c := New(true)
	c.mergedConfig = clientcmdapi.NewConfig()
	c.mergedConfig.Contexts["kc:stays:a.yaml"] = &clientcmdapi.Context{Cluster: "c", AuthInfo: "u"}
	c.mergedConfig.Contexts["kc:new:c.yaml"] = &clientcmdapi.Context{Cluster: "c", AuthInfo: "u"}

	added, removed, err := c.ContextDiff(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0] != "kc:new:c.yaml" {
		t.Errorf("ожидался added=[kc:new:c.yaml], получено %v", added)
	}
	if len(removed) != 1 || removed[0] != "kc:gone:b.yaml" {
		t.Errorf("ожидался removed=[kc:gone:b.yaml], получено %v", removed)
	}
}

func TestIsKubeconfigFile(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name     string
		filename string
		content  string
		expected bool
	}{
		{
			name:     "yaml extension",
			filename: "config.yaml",
			content:  "contexts:\n- name: test",
			expected: true,
		},
		{
			name:     "yml extension",
			filename: "config.yml",
			content:  "contexts:\n- name: test",
			expected: true,
		},
		{
			name:     "config file",
			filename: "config",
			content:  "contexts:\n- name: test",
			expected: true,
		},
		{
			name:     "kubeconfig prefix",
			filename: "kubeconfig-prod",
			content:  "contexts:\n- name: test",
			expected: true,
		},
		{
			name:     "hidden file",
			filename: ".hidden",
			content:  "contexts:\n- name: test",
			expected: false,
		},
		{
			name:     "non-kubeconfig",
			filename: "readme.txt",
			content:  "some text",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(tmpDir, tt.filename)
			if err := os.WriteFile(path, []byte(tt.content), 0600); err != nil {
				t.Fatal(err)
			}

			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}

			result := scanner.IsKubeconfigFile(path, info)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}
