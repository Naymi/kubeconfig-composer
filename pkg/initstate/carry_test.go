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

	s.ApplyNamespaces(cfg)

	if cfg.Contexts["a"].Namespace != "team-a" {
		t.Errorf("namespace a = %q", cfg.Contexts["a"].Namespace)
	}
	if cfg.Contexts["b"].Namespace != "from-source" {
		t.Errorf("пустой прежний namespace не должен затирать значение из источника: %q", cfg.Contexts["b"].Namespace)
	}
}

func TestLostForeign(t *testing.T) {
	s := &Session{Names: map[string]bool{
		"kc:gone:old.yaml": true, // источник удалён: молчим
		"kc:kept:a.yaml":   true,
		"mine":             true, // чужая запись пропала
		"zeta":             true,
	}}
	cfg := clientcmdapi.NewConfig()
	cfg.Contexts["kc:kept:a.yaml"] = &clientcmdapi.Context{}

	got := s.LostForeign(cfg, "kc")
	if want := []string{"mine", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Errorf("с префиксом: got %v, want %v", got, want)
	}

	got = s.LostForeign(cfg, "")
	if want := []string{"kc:gone:old.yaml", "mine", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Errorf("без префикса: got %v, want %v", got, want)
	}
}
