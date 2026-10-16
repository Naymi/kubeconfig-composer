package initstate

import (
	"path/filepath"
	"reflect"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func cfgWith(ctxs ...string) *clientcmdapi.Config {
	cfg := clientcmdapi.NewConfig()
	for _, n := range ctxs {
		cfg.Clusters[n+"-c"] = &clientcmdapi.Cluster{Server: "https://" + n}
		cfg.AuthInfos[n+"-u"] = &clientcmdapi.AuthInfo{Token: n}
		cfg.Contexts[n] = &clientcmdapi.Context{Cluster: n + "-c", AuthInfo: n + "-u"}
	}
	return cfg
}

func TestReadSessionMissingFile(t *testing.T) {
	s, err := ReadSession(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil || s != nil {
		t.Fatalf("s=%v err=%v, ожидалось (nil, nil)", s, err)
	}
}

func TestReadSessionReadsStateAndNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kc.yaml")
	old := cfgWith("a", "b")
	old.CurrentContext = "b"
	old.Contexts["a"].Namespace = "team-a"
	if err := clientcmd.WriteToFile(*old, path); err != nil {
		t.Fatal(err)
	}

	s, err := ReadSession(path)
	if err != nil || s == nil {
		t.Fatalf("s=%v err=%v", s, err)
	}
	if s.CurrentContext != "b" || s.Namespaces["a"] != "team-a" {
		t.Errorf("состояние прочитано неверно: %+v", s)
	}
	for _, n := range []string{"a", "b", "a-c", "b-u"} {
		if !s.Names[n] {
			t.Errorf("имя %q не попало в Names", n)
		}
	}
}

func TestApplyCurrentContextOnlyWhenContextExists(t *testing.T) {
	s := &Session{CurrentContext: "b"}

	withB := cfgWith("a", "b")
	withB.CurrentContext = "a"
	s.ApplyCurrentContext(withB)
	if withB.CurrentContext != "b" {
		t.Errorf("current-context = %q, ожидалось b", withB.CurrentContext)
	}

	noB := cfgWith("a")
	noB.CurrentContext = "a"
	s.ApplyCurrentContext(noB)
	if noB.CurrentContext != "a" {
		t.Errorf("current-context = %q, должен остаться a: прежнего контекста больше нет", noB.CurrentContext)
	}
}

func TestApplyNamespacesSkipsEmptyAndMissing(t *testing.T) {
	s := &Session{Namespaces: map[string]string{"a": "team-a", "b": "", "gone": "x"}}
	cfg := cfgWith("a", "b")
	cfg.Contexts["b"].Namespace = "from-source"

	s.ApplyNamespaces(cfg, nil)

	if cfg.Contexts["a"].Namespace != "team-a" {
		t.Errorf("namespace a = %q", cfg.Contexts["a"].Namespace)
	}
	if cfg.Contexts["b"].Namespace != "from-source" {
		t.Errorf("пустой прежний namespace не должен затирать значение из источника: %q", cfg.Contexts["b"].Namespace)
	}
}

func TestApplyNamespacesSourceWinsWhenUserDidNotChangeIt(t *testing.T) {
	// a: в прежнем файле namespace тот, что прошлый раз дал источник, — его
	// никто не трогал, новое значение из источника побеждает.
	// b: пользователь сменил namespace (kubens) — его выбор сохраняется.
	// c: контекста не было в прошлой сборке — переносим прежний, как раньше.
	s := &Session{Namespaces: map[string]string{"a": "old-ns", "b": "mine", "c": "kept"}}
	prevSource := map[string]string{"a": "old-ns", "b": "old-b"}
	cfg := cfgWith("a", "b", "c")
	cfg.Contexts["a"].Namespace = "new-ns"
	cfg.Contexts["b"].Namespace = "new-b"
	cfg.Contexts["c"].Namespace = "from-source"

	s.ApplyNamespaces(cfg, prevSource)

	if got := cfg.Contexts["a"].Namespace; got != "new-ns" {
		t.Errorf("a = %q, ожидалось new-ns", got)
	}
	if got := cfg.Contexts["b"].Namespace; got != "mine" {
		t.Errorf("b = %q, ожидалось mine", got)
	}
	if got := cfg.Contexts["c"].Namespace; got != "kept" {
		t.Errorf("c = %q, ожидалось kept", got)
	}
}

func TestLostForeign(t *testing.T) {
	s := &Session{Names: map[string]bool{
		"kc:gone:old.yaml": true, // записан прошлой сборкой, источник удалён: молчим
		"kc:kept:a.yaml":   true,
		"kc:added:x.yaml":  true, // с префиксом, но дописан не нами
		"mine":             true, // чужая запись пропала
		"zeta":             true,
	}}
	cfg := clientcmdapi.NewConfig()
	cfg.Contexts["kc:kept:a.yaml"] = &clientcmdapi.Context{}
	owned := map[string]bool{"kc:gone:old.yaml": true, "kc:kept:a.yaml": true}

	got := s.LostForeign(cfg, owned)
	if want := []string{"kc:added:x.yaml", "mine", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Errorf("с прошлой сборкой: got %v, want %v", got, want)
	}

	got = s.LostForeign(cfg, nil)
	if want := []string{"kc:added:x.yaml", "kc:gone:old.yaml", "mine", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Errorf("без состояния: got %v, want %v", got, want)
	}
}
