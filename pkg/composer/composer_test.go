package composer

import (
	"os"
	"path/filepath"
	"testing"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestGetUniqueName(t *testing.T) {
	c := New()

	name1 := c.getUniqueName("prod", "context")
	if name1 != "prod" {
		t.Errorf("Expected 'prod', got '%s'", name1)
	}

	name2 := c.getUniqueName("prod", "context")
	if name2 != "prod-1" {
		t.Errorf("Expected 'prod-1', got '%s'", name2)
	}

	name3 := c.getUniqueName("prod", "context")
	if name3 != "prod-2" {
		t.Errorf("Expected 'prod-2', got '%s'", name3)
	}
}

func TestMergeConfig(t *testing.T) {
	c := New()

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

	c.configs = []*clientcmdapi.Config{config1, config2}
	c.Merge()

	if len(c.mergedConfig.Contexts) != 2 {
		t.Errorf("Expected 2 contexts, got %d", len(c.mergedConfig.Contexts))
	}

	if _, exists := c.mergedConfig.Contexts["context1"]; !exists {
		t.Error("Expected context1 to exist")
	}

	if _, exists := c.mergedConfig.Contexts["context1-1"]; !exists {
		t.Error("Expected context1-1 to exist")
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

			result := isKubeconfigFile(path, info)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}
