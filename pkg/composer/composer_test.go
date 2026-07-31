package composer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/naymi/kubeconfig-composer/pkg/scanner"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestGetUniqueName(t *testing.T) {
	c := New(true) // autoAccept = true для тестов

	name1 := c.getUniqueName("prod", "context", "source1", "/path/to/file1", "prod")
	if name1 != "prod" {
		t.Errorf("Expected 'prod', got '%s'", name1)
	}

	name2 := c.getUniqueName("prod", "context", "source2", "/path/to/file2", "prod")
	if name2 == "prod" {
		t.Errorf("Expected unique name, got '%s'", name2)
	}

	name3 := c.getUniqueName("prod", "context", "source3", "/path/to/file3", "prod")
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

	if len(c.mergedConfig.Contexts) < 1 {
		t.Errorf("Expected at least 1 context, got %d", len(c.mergedConfig.Contexts))
	}

	if _, exists := c.mergedConfig.Contexts["context1"]; !exists {
		t.Error("Expected context1 to exist")
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
	// существующий конфиг + тот же исходник) не должны плодить дубликаты.
	c := New(true)
	c.configs = []configWithSource{
		{config: makeConfig("ctx", "c1", "u1", "https://x", "t"), source: "a", fullPath: "/a"},
		{config: makeConfig("ctx", "c1", "u1", "https://x", "t"), source: "b", fullPath: "/b"},
	}
	if err := c.Merge(); err != nil {
		t.Fatal(err)
	}
	if len(c.mergedConfig.Clusters) != 1 || len(c.mergedConfig.AuthInfos) != 1 || len(c.mergedConfig.Contexts) != 1 {
		t.Fatalf("идентичные записи должны переиспользоваться: clusters=%d users=%d contexts=%d",
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
