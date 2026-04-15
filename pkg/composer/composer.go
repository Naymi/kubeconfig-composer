package composer

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type configWithSource struct {
	config   *clientcmdapi.Config
	source   string // имя файла без расширения
	fullPath string // полный путь к файлу
}

type Composer struct {
	configs        []configWithSource
	mergedConfig   *clientcmdapi.Config
	clusterCounts  map[string]int
	userCounts     map[string]int
	contextCounts  map[string]int
	clusterSources map[string]nameSource
	userSources    map[string]nameSource
	contextSources map[string]nameSource
}

type nameSource struct {
	filePath string
	itemType string // "cluster", "user", "context"
	itemName string // оригинальное имя элемента
}

func New() *Composer {
	return &Composer{
		configs:        make([]configWithSource, 0),
		clusterCounts:  make(map[string]int),
		userCounts:     make(map[string]int),
		contextCounts:  make(map[string]int),
		clusterSources: make(map[string]nameSource),
		userSources:    make(map[string]nameSource),
		contextSources: make(map[string]nameSource),
	}
}

func (c *Composer) LoadFiles(files []string) error {
	type loadResult struct {
		config configWithSource
		err    error
		path   string
	}

	resultChan := make(chan loadResult, len(files))

	// Загружаем файлы параллельно
	for _, path := range files {
		go func(p string) {
			config, err := clientcmd.LoadFromFile(p)
			if err != nil {
				resultChan <- loadResult{err: err, path: p}
				return
			}

			absPath, _ := filepath.Abs(p)
			source := getFileBaseName(p)
			resultChan <- loadResult{
				config: configWithSource{
					config:   config,
					source:   source,
					fullPath: absPath,
				},
				path: p,
			}
		}(path)
	}

	// Собираем результаты
	for i := 0; i < len(files); i++ {
		result := <-resultChan
		if result.err != nil {
			return fmt.Errorf("failed to load %s: %w", result.path, result.err)
		}
		c.configs = append(c.configs, result.config)
		fmt.Printf("Loaded kubeconfig: %s\n", result.path)
	}
	close(resultChan)

	return nil
}

func (c *Composer) ScanDirectory(dir string) error {
	var kubeconfigPaths []string

	// Сначала собираем все пути к файлам
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		if !isKubeconfigFile(path, info) {
			return nil
		}

		kubeconfigPaths = append(kubeconfigPaths, path)
		return nil
	})

	if err != nil {
		return err
	}

	if len(kubeconfigPaths) == 0 {
		return nil
	}

	// Загружаем файлы параллельно
	type loadResult struct {
		config configWithSource
		err    error
		path   string
	}

	resultChan := make(chan loadResult, len(kubeconfigPaths))

	for _, path := range kubeconfigPaths {
		go func(p string) {
			config, err := clientcmd.LoadFromFile(p)
			if err != nil {
				resultChan <- loadResult{err: err, path: p}
				return
			}

			absPath, _ := filepath.Abs(p)
			source := getFileBaseName(p)
			resultChan <- loadResult{
				config: configWithSource{
					config:   config,
					source:   source,
					fullPath: absPath,
				},
				path: p,
			}
		}(path)
	}

	// Собираем результаты
	for i := 0; i < len(kubeconfigPaths); i++ {
		result := <-resultChan
		if result.err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to load %s: %v\n", result.path, result.err)
			continue
		}
		c.configs = append(c.configs, result.config)
		fmt.Printf("Found kubeconfig: %s\n", result.path)
	}
	close(resultChan)

	return nil
}

func (c *Composer) Merge() error {
	c.mergedConfig = clientcmdapi.NewConfig()

	for _, configWithSrc := range c.configs {
		c.mergeConfig(configWithSrc.config, configWithSrc.source, configWithSrc.fullPath)
	}

	return nil
}

func (c *Composer) mergeConfig(config *clientcmdapi.Config, source, fullPath string) {
	// Сначала обрабатываем кластеры и пользователей, сохраняя маппинг старых имён на новые
	clusterMapping := make(map[string]string)
	userMapping := make(map[string]string)

	for name, cluster := range config.Clusters {
		uniqueName := c.getUniqueName(name, "cluster", source, fullPath, name)
		c.mergedConfig.Clusters[uniqueName] = cluster
		clusterMapping[name] = uniqueName
	}

	for name, authInfo := range config.AuthInfos {
		uniqueName := c.getUniqueName(name, "user", source, fullPath, name)
		c.mergedConfig.AuthInfos[uniqueName] = authInfo
		userMapping[name] = uniqueName
	}

	// Затем обрабатываем контексты, используя уже созданный маппинг
	for name, context := range config.Contexts {
		uniqueName := c.getUniqueName(name, "context", source, fullPath, name)

		newContext := context.DeepCopy()
		// Используем маппинг вместо повторного вызова getUniqueName
		if mappedCluster, ok := clusterMapping[context.Cluster]; ok {
			newContext.Cluster = mappedCluster
		}
		if mappedUser, ok := userMapping[context.AuthInfo]; ok {
			newContext.AuthInfo = mappedUser
		}

		c.mergedConfig.Contexts[uniqueName] = newContext
	}
}

func (c *Composer) getUniqueName(name, prefix, source, fullPath, originalItemName string) string {
	// Если имя "Default", используем имя файла
	originalName := name
	if strings.ToLower(name) == "default" {
		name = source
	}

	// Выбираем правильные мапы в зависимости от типа элемента
	var counts map[string]int
	var sources map[string]nameSource

	switch prefix {
	case "cluster":
		counts = c.clusterCounts
		sources = c.clusterSources
	case "user":
		counts = c.userCounts
		sources = c.userSources
	case "context":
		counts = c.contextCounts
		sources = c.contextSources
	default:
		return name
	}

	if _, exists := counts[name]; !exists {
		counts[name] = 0
		sources[name] = nameSource{
			filePath: fullPath,
			itemType: prefix,
			itemName: originalName,
		}
		return name
	}

	// При конфликте показываем оба элемента и предлагаем ввести имя
	counts[name]++

	firstSource := sources[name]

	// Формируем предложение на основе имени файла
	suggestedName := fmt.Sprintf("%s-%s", originalItemName, source)

	fmt.Printf("\n⚠️  Конфликт имени '%s' для %s (попытка #%d)\n", name, prefix, counts[name])
	fmt.Printf("  Первый:  %s '%s' из %s\n", firstSource.itemType, firstSource.itemName, firstSource.filePath)
	fmt.Printf("  Текущий: %s '%s' из %s\n", prefix, originalName, fullPath)
	fmt.Printf("Введите новое имя для %s (или нажмите Enter для автоматического: %s): ", prefix, suggestedName)

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil || strings.TrimSpace(input) == "" {
		// Проверяем, не занято ли предложенное имя
		if _, exists := counts[suggestedName]; !exists {
			counts[suggestedName] = 0
			sources[suggestedName] = nameSource{
				filePath: fullPath,
				itemType: prefix,
				itemName: originalName,
			}
			return suggestedName
		}
		// Если занято, используем старый вариант с номером
		return fmt.Sprintf("%s-%d", name, counts[name])
	}

	customName := strings.TrimSpace(input)

	// Проверяем, не занято ли введённое имя
	if _, exists := counts[customName]; exists {
		fmt.Printf("⚠️  Имя '%s' уже используется, применяем автоматическое имя\n", customName)
		if _, exists := counts[suggestedName]; !exists {
			return suggestedName
		}
		return fmt.Sprintf("%s-%d", name, counts[name])
	}

	counts[customName] = 0
	sources[customName] = nameSource{
		filePath: fullPath,
		itemType: prefix,
		itemName: originalName,
	}
	return customName
}

func (c *Composer) WriteOutput(path string) error {
	data, err := clientcmd.Write(*c.mergedConfig)
	if err != nil {
		return fmt.Errorf("failed to serialize config: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

func getFileBaseName(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	if ext != "" {
		return strings.TrimSuffix(base, ext)
	}
	return base
}

func isKubeconfigFile(path string, info os.FileInfo) bool {
	name := info.Name()

	if strings.HasPrefix(name, ".") {
		return false
	}

	if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
		return true
	}

	if name == "config" || strings.HasPrefix(name, "kubeconfig") {
		return true
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	var data map[string]interface{}
	if err := yaml.Unmarshal(content, &data); err != nil {
		return false
	}

	_, hasContexts := data["contexts"]
	_, hasClusters := data["clusters"]
	_, hasUsers := data["users"]

	return hasContexts || hasClusters || hasUsers
}
