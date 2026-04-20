package composer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/naymi/kubeconfig-composer/pkg/scanner"
	"github.com/pterm/pterm"
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
	autoAccept     bool
	conflicts      []conflictResolution
}

type conflictResolution struct {
	configName  string // имя конфига (источника)
	clusterOld  string
	clusterNew  string
	userOld     string
	userNew     string
	contextOld  string
	contextNew  string
	source      string
}

type nameSource struct {
	filePath string
	itemType string // "cluster", "user", "context"
	itemName string // оригинальное имя элемента
}

func New(autoAccept bool) *Composer {
	return &Composer{
		configs:        make([]configWithSource, 0),
		clusterCounts:  make(map[string]int),
		userCounts:     make(map[string]int),
		contextCounts:  make(map[string]int),
		clusterSources: make(map[string]nameSource),
		userSources:    make(map[string]nameSource),
		contextSources: make(map[string]nameSource),
		autoAccept:     autoAccept,
		conflicts:      make([]conflictResolution, 0),
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

		if !scanner.IsKubeconfigFile(path, info) {
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
			pterm.FgYellow.Printf("⚠ Не удалось загрузить %s: %v\n", result.path, result.err)
			continue
		}
		c.configs = append(c.configs, result.config)
	}
	close(resultChan)

	return nil
}

func (c *Composer) Merge() error {
	c.mergedConfig = clientcmdapi.NewConfig()

	// Первый проход - собираем конфликты
	for _, configWithSrc := range c.configs {
		c.detectConflicts(configWithSrc.config, configWithSrc.source, configWithSrc.fullPath)
	}

	// Если есть конфликты и не включен autoAccept, показываем таблицу и спрашиваем
	if len(c.conflicts) > 0 && !c.autoAccept {
		if !c.showConflictsAndAsk() {
			// Пользователь отказался от автоматического применения
			c.autoAccept = false
		} else {
			c.autoAccept = true
		}
	}

	// Второй проход - выполняем merge с учетом решения
	c.clusterCounts = make(map[string]int)
	c.userCounts = make(map[string]int)
	c.contextCounts = make(map[string]int)
	c.clusterSources = make(map[string]nameSource)
	c.userSources = make(map[string]nameSource)
	c.contextSources = make(map[string]nameSource)

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

	// Если включен режим автоматического принятия
	if c.autoAccept {
		// Проверяем, не занято ли предложенное имя
		if _, exists := counts[suggestedName]; !exists {
			counts[suggestedName] = 0
			sources[suggestedName] = nameSource{
				filePath: fullPath,
				itemType: prefix,
				itemName: originalName,
			}
			pterm.FgCyan.Printf("→ Автоматически принято: %s → %s\n", name, suggestedName)
			return suggestedName
		}
		// Если занято, используем числовой суффикс
		finalName := fmt.Sprintf("%s-%d", name, counts[name])
		counts[finalName] = 0
		sources[finalName] = nameSource{
			filePath: fullPath,
			itemType: prefix,
			itemName: originalName,
		}
		pterm.FgCyan.Printf("→ Автоматически принято: %s → %s\n", name, finalName)
		return finalName
	}

	fmt.Println()
	pterm.FgYellow.Printf("⚠ Конфликт '%s' для %s\n", name, prefix)
	pterm.DefaultBasicText.Printf("  Первый:  %s из %s\n", firstSource.itemName, firstSource.filePath)
	pterm.DefaultBasicText.Printf("  Текущий: %s из %s\n", originalName, fullPath)
	fmt.Printf("Новое имя [%s]: ", suggestedName)

	var customName string
	fmt.Scanln(&customName)
	customName = strings.TrimSpace(customName)

	if customName == "" {
		customName = suggestedName
	}

	// Проверяем, не занято ли введённое имя
	if _, exists := counts[customName]; exists {
		pterm.FgYellow.Printf("⚠ Имя '%s' занято, используем: %s\n", customName, suggestedName)
		if _, exists := counts[suggestedName]; !exists {
			counts[suggestedName] = 0
			sources[suggestedName] = nameSource{
				filePath: fullPath,
				itemType: prefix,
				itemName: originalName,
			}
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

func (c *Composer) GetLoadedFiles() []configWithSource {
	return c.configs
}

func getFileBaseName(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	if ext != "" {
		return strings.TrimSuffix(base, ext)
	}
	return base
}

func (c *Composer) detectConflicts(config *clientcmdapi.Config, source, fullPath string) {
	// Группируем конфликты по контексту
	contextConflicts := make(map[string]*conflictResolution)

	tempClusterCounts := make(map[string]int)
	tempUserCounts := make(map[string]int)
	tempContextCounts := make(map[string]int)

	// Копируем текущие счетчики
	for k, v := range c.clusterCounts {
		tempClusterCounts[k] = v
	}
	for k, v := range c.userCounts {
		tempUserCounts[k] = v
	}
	for k, v := range c.contextCounts {
		tempContextCounts[k] = v
	}

	// Проверяем контексты и связанные кластеры/пользователей
	for contextName, context := range config.Contexts {
		originalContextName := contextName
		if strings.ToLower(contextName) == "default" {
			contextName = source
		}

		clusterName := context.Cluster
		originalClusterName := clusterName
		if strings.ToLower(clusterName) == "default" {
			clusterName = source
		}

		userName := context.AuthInfo
		originalUserName := userName
		if strings.ToLower(userName) == "default" {
			userName = source
		}

		conflict := &conflictResolution{
			configName: source,
			source:     fullPath,
		}

		hasConflict := false

		// Проверяем кластер
		if _, exists := tempClusterCounts[clusterName]; exists {
			tempClusterCounts[clusterName]++
			conflict.clusterOld = clusterName
			conflict.clusterNew = fmt.Sprintf("%s-%s", originalClusterName, source)
			hasConflict = true
		} else {
			tempClusterCounts[clusterName] = 0
		}

		// Проверяем пользователя
		if _, exists := tempUserCounts[userName]; exists {
			tempUserCounts[userName]++
			conflict.userOld = userName
			conflict.userNew = fmt.Sprintf("%s-%s", originalUserName, source)
			hasConflict = true
		} else {
			tempUserCounts[userName] = 0
		}

		// Проверяем контекст
		if _, exists := tempContextCounts[contextName]; exists {
			tempContextCounts[contextName]++
			conflict.contextOld = contextName
			conflict.contextNew = fmt.Sprintf("%s-%s", originalContextName, source)
			hasConflict = true
		} else {
			tempContextCounts[contextName] = 0
		}

		if hasConflict {
			contextConflicts[originalContextName] = conflict
		}
	}

	// Добавляем конфликты в список
	for _, conflict := range contextConflicts {
		c.conflicts = append(c.conflicts, *conflict)
	}

	// Обновляем основные счетчики
	c.clusterCounts = tempClusterCounts
	c.userCounts = tempUserCounts
	c.contextCounts = tempContextCounts
}

func (c *Composer) showConflictsAndAsk() bool {
	fmt.Println()
	pterm.FgYellow.Println("⚠ Обнаружены конфликты имён")
	fmt.Println()

	// Группируем конфликты по источнику
	conflictsBySource := make(map[string][]conflictResolution)
	for _, conflict := range c.conflicts {
		conflictsBySource[conflict.source] = append(conflictsBySource[conflict.source], conflict)
	}

	// Создаем одну таблицу со всеми конфликтами
	tableData := pterm.TableData{}

	first := true
	for source, conflicts := range conflictsBySource {
		// Добавляем пустую строку-разделитель между конфигами
		if !first {
			tableData = append(tableData, []string{"", "", ""})
		}
		first = false

		// Добавляем строку с путем к конфигу
		tableData = append(tableData, []string{source, "", ""})

		// Добавляем заголовок для этой секции
		tableData = append(tableData, []string{"Тип", "До", "После"})

		// Добавляем конфликты
		for _, conflict := range conflicts {
			if conflict.clusterOld != "" {
				tableData = append(tableData, []string{
					"cluster",
					conflict.clusterOld,
					conflict.clusterNew,
				})
			}
			if conflict.userOld != "" {
				tableData = append(tableData, []string{
					"user",
					conflict.userOld,
					conflict.userNew,
				})
			}
			if conflict.contextOld != "" {
				tableData = append(tableData, []string{
					"context",
					conflict.contextOld,
					conflict.contextNew,
				})
			}
		}
	}

	pterm.DefaultTable.WithHasHeader(false).WithBoxed(true).WithData(tableData).Render()
	fmt.Println()

	fmt.Print("Применить эти имена автоматически? [Y/n]: ")

	var response string
	fmt.Scanln(&response)
	response = strings.TrimSpace(strings.ToLower(response))

	return response == "" || response == "y" || response == "yes" || response == "д" || response == "да"
}

