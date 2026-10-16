package initstate

import "testing"

func TestShellExportQuoting(t *testing.T) {
	cases := map[string]string{
		"/home/u/.kube/kc.yaml":    `export KUBECONFIG='/home/u/.kube/kc.yaml'`,
		"/home/my user/kc.yaml":    `export KUBECONFIG='/home/my user/kc.yaml'`,
		"/home/o'brien/kc.yaml":    `export KUBECONFIG='/home/o'\''brien/kc.yaml'`,
		"/home/$(rm -rf x)/kc.yml": `export KUBECONFIG='/home/$(rm -rf x)/kc.yml'`,
	}
	for in, want := range cases {
		if got := ShellExport(in); got != want {
			t.Errorf("ShellExport(%q) = %s, want %s", in, got, want)
		}
	}
}
