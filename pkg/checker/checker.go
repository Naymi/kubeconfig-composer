package checker

import (
	"context"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// ClusterStatus представляет статус подключения к кластеру
type ClusterStatus struct {
	Context string
	Cluster string
	Status  string
	Version string
	Error   string
}

// CheckClusterStatus проверяет доступность кластера
func CheckClusterStatus(kubeconfigPath, contextName, clusterName string, timeout int) ClusterStatus {
	status := ClusterStatus{
		Context: contextName,
		Cluster: clusterName,
		Status:  "✗",
		Version: "-",
		Error:   "",
	}

	// Создаём конфигурацию для конкретного контекста
	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath}
	configOverrides := &clientcmd.ConfigOverrides{CurrentContext: contextName}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	restConfig, err := kubeConfig.ClientConfig()
	if err != nil {
		status.Error = TruncateError(err.Error())
		return status
	}

	// Устанавливаем таймаут
	restConfig.Timeout = time.Duration(timeout) * time.Second

	// Создаём клиент
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		status.Error = TruncateError(err.Error())
		return status
	}

	// Пробуем получить версию сервера
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	versionInfo, err := clientset.Discovery().ServerVersion()
	if err != nil {
		status.Error = TruncateError(err.Error())
		return status
	}

	// Проверяем, что можем получить список нод (базовая проверка доступа)
	_, err = clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		// Если не можем получить ноды, но версия получена - всё равно считаем успехом
		// (может быть ограничение прав доступа)
		status.Status = "⚠"
		status.Version = versionInfo.GitVersion
		status.Error = "ограниченный доступ"
		return status
	}

	status.Status = "✓"
	status.Version = versionInfo.GitVersion
	return status
}

// TruncateError сокращает текст ошибки для удобного отображения
func TruncateError(err string) string {
	// Убираем лишние детали из ошибки
	err = strings.ReplaceAll(err, "\n", " ")

	// Извлекаем основную причину
	if strings.Contains(err, "connection refused") {
		return "connection refused"
	}
	if strings.Contains(err, "timeout") {
		return "timeout"
	}
	if strings.Contains(err, "no such host") {
		return "no such host"
	}
	if strings.Contains(err, "certificate") {
		return "certificate error"
	}
	if strings.Contains(err, "unauthorized") {
		return "unauthorized"
	}
	if strings.Contains(err, "forbidden") {
		return "forbidden"
	}

	// Обрезаем длинные ошибки
	if len(err) > 50 {
		return err[:47] + "..."
	}
	return err
}
