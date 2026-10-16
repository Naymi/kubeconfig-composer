package scanner

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func writeKubeconfig(t *testing.T, path string) {
	t.Helper()
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["c"] = &clientcmdapi.Cluster{Server: "https://example.invalid"}
	cfg.AuthInfos["u"] = &clientcmdapi.AuthInfo{Token: "t"}
	cfg.Contexts["ctx"] = &clientcmdapi.Context{Cluster: "c", AuthInfo: "u"}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := clientcmd.WriteToFile(*cfg, path); err != nil {
		t.Fatal(err)
	}
}

func TestIsSkippedDir(t *testing.T) {
	cases := map[string]bool{
		"cache":      true,
		"http-cache": true,
		".git":       true,
		".hidden":    true,
		"prod":       false,
		"caches":     false,
		"kubens":     true,
		"kubens-old": false,
	}
	for name, want := range cases {
		if got := IsSkippedDir(name); got != want {
			t.Errorf("IsSkippedDir(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestIsServiceFile(t *testing.T) {
	cases := map[string]bool{
		"kubectx":      true,
		"config.lock":  true,
		"kc.yaml.lock": true,
		".lock":        true,
		"config":       false,
		"kc.yaml":      false,
		"kubectx.yaml": false,
		"kubens":       false,
		"lock.yaml":    false,
		"locked":       false,
	}
	for name, want := range cases {
		if got := IsServiceFile(name); got != want {
			t.Errorf("IsServiceFile(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestIsKubeconfigFileRejectsServiceFiles(t *testing.T) {
	dir := t.TempDir()
	// Содержимое — настоящий kubeconfig: отказ должен идти по имени, а не по
	// содержимому (config.lock к тому же подходил бы под проверку содержимого).
	for _, name := range []string{"kubectx", "config.lock", "kc.yaml.lock"} {
		path := filepath.Join(dir, name)
		writeKubeconfig(t, path)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if IsKubeconfigFile(path, info) {
			t.Errorf("IsKubeconfigFile(%q) = true, служебный файл не источник", name)
		}
	}
}

func TestFindKubeconfigFilesSkipsServiceDirs(t *testing.T) {
	// Корень скрытый, как настоящий ~/.kube: его пропускать нельзя.
	root := filepath.Join(t.TempDir(), ".kube")
	writeKubeconfig(t, filepath.Join(root, "config"))
	writeKubeconfig(t, filepath.Join(root, "nested", "ok.yaml"))
	writeKubeconfig(t, filepath.Join(root, "cache", "http", "x.yaml"))
	writeKubeconfig(t, filepath.Join(root, "http-cache", "y.yaml"))
	writeKubeconfig(t, filepath.Join(root, ".git", "z.yaml"))

	files, err := FindKubeconfigFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	var rel []string
	for _, f := range files {
		r, _ := filepath.Rel(root, f)
		rel = append(rel, r)
	}
	sort.Strings(rel)
	want := []string{"config", filepath.Join("nested", "ok.yaml")}
	if len(rel) != len(want) || rel[0] != want[0] || rel[1] != want[1] {
		t.Fatalf("got %v, want %v", rel, want)
	}
}
