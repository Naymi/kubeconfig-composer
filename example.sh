#!/bin/bash

# Пример использования kubeconfig-composer

echo "=== Kubeconfig Composer Demo ==="
echo

# Создаём тестовую директорию
TEST_DIR="/tmp/kube-test"
mkdir -p "$TEST_DIR/prod" "$TEST_DIR/dev"

# Создаём первый kubeconfig
cat > "$TEST_DIR/config" <<EOF
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://prod-cluster.example.com
  name: prod-cluster
contexts:
- context:
    cluster: prod-cluster
    user: admin
  name: prod
current-context: prod
users:
- name: admin
  user:
    token: prod-token-123
EOF

# Создаём второй kubeconfig с тем же именем контекста
cat > "$TEST_DIR/prod/kubeconfig.yaml" <<EOF
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://prod-cluster-2.example.com
  name: prod-cluster
contexts:
- context:
    cluster: prod-cluster
    user: admin
  name: prod
current-context: prod
users:
- name: admin
  user:
    token: prod-token-456
EOF

# Создаём третий kubeconfig
cat > "$TEST_DIR/dev/config.yml" <<EOF
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://dev-cluster.example.com
  name: dev-cluster
contexts:
- context:
    cluster: dev-cluster
    user: developer
  name: dev
current-context: dev
users:
- name: developer
  user:
    token: dev-token-789
EOF

echo "Созданы тестовые kubeconfig файлы в $TEST_DIR"
echo

# Запускаем утилиту
echo "Запуск kubeconfig-composer..."
./kubeconfig-composer -dir "$TEST_DIR" -output "$TEST_DIR/merged.yaml"

echo
echo "Результат:"
echo "=========="
kubectl config view --kubeconfig="$TEST_DIR/merged.yaml"

echo
echo "Контексты в объединённом файле:"
kubectl config get-contexts --kubeconfig="$TEST_DIR/merged.yaml"

echo
echo "Очистка..."
rm -rf "$TEST_DIR"
echo "Готово!"
