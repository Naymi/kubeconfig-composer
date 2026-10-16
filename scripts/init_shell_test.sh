#!/bin/sh
# Проверка строки из .bashrc/.zshrc в настоящих bash и zsh. Запускается в
# контейнере (task test:container): kubeconfig-composer должен быть в PATH.
set -eu

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
export HOME="$WORK/home user" # пробел в пути: export должен выдержать eval
mkdir -p "$HOME/.kube"

cat > "$HOME/.kube/dev.yaml" <<'YAML'
apiVersion: v1
kind: Config
clusters:
- name: dev-c
  cluster:
    server: https://dev.invalid
users:
- name: dev-u
  user:
    token: t
contexts:
- name: dev
  context:
    cluster: dev-c
    user: dev-u
current-context: dev
YAML

FAILED=0
check() { # описание, ожидаемое, фактическое
	if [ "$2" = "$3" ]; then
		echo "ok   $1"
	else
		echo "FAIL $1: ожидалось '$2', получено '$3'"
		FAILED=1
	fi
}

for sh_bin in bash zsh; do
	# shellcheck disable=SC2016 # одинарные кавычки намеренны: подстановки выполняет вложенный шелл
	got="$($sh_bin -c 'eval "$(kubeconfig-composer init)"; printf %s "$KUBECONFIG"')"
	check "$sh_bin выставляет KUBECONFIG" "$HOME/.kube/kc.yaml" "$got"
done

got="$(KUBECONFIG="$HOME/.kube/kc.yaml" kubeconfig-composer list --dir "$HOME/.kube" 2>&1 | grep -c 'kc.yaml' || true)"
check "list видит kc.yaml как обычный файл, шелл не сломан" "1" "$got"

# Сбой init не должен ломать запуск оболочки: eval пустого stdout, дальше всё работает.
for sh_bin in bash zsh; do
	# shellcheck disable=SC2016 # одинарные кавычки намеренны: подстановки выполняет вложенный шелл
	got="$(HOME="$WORK/empty-home" $sh_bin -c 'eval "$(kubeconfig-composer init --dir /nonexistent --output /tmp/none/kc.yaml 2>/dev/null)"; echo alive')"
	check "$sh_bin живёт после сбоя init" "alive" "$got"
done

exit "$FAILED"
