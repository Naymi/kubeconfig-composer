package composer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/naymi/kubeconfig-composer/pkg/cleaner"
	"github.com/naymi/kubeconfig-composer/pkg/scanner"
	"github.com/olekukonko/tablewriter"
	"github.com/pterm/pterm"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type configWithSource struct {
	config   *clientcmdapi.Config
	source   string // имя файла без расширения
	fullPath string // полный путь к файлу
}

func (c *configWithSource) GetFullPath() string {
	return c.fullPath
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
	// Первый увиденный объект под каждым именем — чтобы отличать настоящий
	// конфликт (то же имя, другое содержимое) от повторного включения того же
	// самого (то же имя, идентичное содержимое).
	clusterObjs map[string]*clientcmdapi.Cluster
	userObjs    map[string]*clientcmdapi.AuthInfo
	contextObjs map[string]*clientcmdapi.Context
	autoAccept  bool
	conflicts   []conflictResolution
	// baseDir — директория (--dir), относительно которой путь к файлу
	// добавляется в имя сущности (см. SetBaseDir, relativeSourcePath).
	baseDir string
	// namePrefix добавляется перед именем каждой смерженной из файла сущности
	// (см. SetNamePrefix), например "kc:prod (nested/config.yaml)".
	namePrefix string
}

type conflictResolution struct {
	configName string // имя конфига (источника)
	clusterOld string
	clusterNew string
	userOld    string
	userNew    string
	contextOld string
	contextNew string
	source     string
}

type nameSource struct {
	filePath string
	itemType string // "cluster", "user", "context"
	itemName string // оригинальное имя элемента
}

// DefaultNamePrefix — префикс по умолчанию в имени каждой смерженной из
// файла сущности (см. SetNamePrefix), формат "<prefix>:<имя>:<путь>".
// Настраивается флагом --prefix команды merge.
const DefaultNamePrefix = "kc"

func New(autoAccept bool) *Composer {
	return &Composer{
		configs:        make([]configWithSource, 0),
		clusterCounts:  make(map[string]int),
		userCounts:     make(map[string]int),
		contextCounts:  make(map[string]int),
		clusterSources: make(map[string]nameSource),
		userSources:    make(map[string]nameSource),
		contextSources: make(map[string]nameSource),
		clusterObjs:    make(map[string]*clientcmdapi.Cluster),
		userObjs:       make(map[string]*clientcmdapi.AuthInfo),
		contextObjs:    make(map[string]*clientcmdapi.Context),
		autoAccept:     autoAccept,
		conflicts:      make([]conflictResolution, 0),
		namePrefix:     DefaultNamePrefix,
	}
}

// SetBaseDir задаёт директорию (--dir), относительно которой путь к файлу
// добавляется в имя сущности — вместо абсолютного пути файловой системы,
// который был бы бесполезен на другой машине/у другого пользователя.
func (c *Composer) SetBaseDir(dir string) {
	c.baseDir = dir
}

// SetNamePrefix задаёт префикс, который добавляется перед именем каждой
// смерженной из файла сущности (кластера/пользователя/контекста). Пустая
// строка отключает префикс.
func (c *Composer) SetNamePrefix(prefix string) {
	c.namePrefix = prefix
}

// relativeSourcePath переводит абсолютный путь файла в путь относительно
// baseDir. Если baseDir не задан или путь не удаётся сделать относительным,
// возвращает исходный абсолютный путь как есть.
func (c *Composer) relativeSourcePath(fullPath string) string {
	if c.baseDir == "" {
		return fullPath
	}
	absBase, err := filepath.Abs(c.baseDir)
	if err != nil {
		return fullPath
	}
	rel, err := filepath.Rel(absBase, fullPath)
	if err != nil {
		return fullPath
	}
	return rel
}

// effectiveName возвращает имя, которое реально используется для сущности
// (кластера, пользователя или контекста) при merge, в формате
// "<prefix>:<имя>:<путь>" (если namePrefix пустой — просто "<имя>:<путь>"):
//   - <имя> — исходное имя сущности; если оно типовое (isGenericName) и не
//     несёт смысла (default, kubernetes-admin, local, user, ...), вместо него
//     подставляется имя файла-источника без расширения (source);
//   - <путь> — путь к файлу-источнику относительно --dir
//     (см. SetBaseDir/relativeSourcePath).
//
// Например: "kc:prod:nested/config.yaml" (осмысленное имя) или
// "kc:satellite03:satellite03.conf" (типовое имя заменено на файл). Второе
// возвращаемое значение — была ли произведена подстановка (нужно, чтобы при
// конфликте использовать числовой суффикс "_N", а не "-source").
//
// Если namePrefix задан и имя уже начинается с "<prefix>:", значит оно уже
// было подставлено на прошлом merge (запись перезагружена из ранее
// записанного output) — трогать повторно не нужно, иначе префикс наслаивался
// бы на каждом запуске, ломая идемпотентность. Если namePrefix пустой,
// такого надёжного маркера нет — на перезагрузке базового файла запись может
// транзитно задвоиться, но Composer.Dedupe(), которым merge всегда завершает
// пайплайн, схлопывает дубли обратно по содержимому.
func (c *Composer) effectiveName(name, source, fullPath string) (string, bool) {
	if c.namePrefix != "" && strings.HasPrefix(name, c.namePrefix+":") {
		return name, false
	}
	entity := name
	if isGenericName(name) {
		entity = source
	}
	relPath := c.relativeSourcePath(fullPath)
	if c.namePrefix == "" {
		return fmt.Sprintf("%s:%s", entity, relPath), true
	}
	return fmt.Sprintf("%s:%s:%s", c.namePrefix, entity, relPath), true
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

func (c *Composer) ScanDirectory(dir string, exclude ...string) error {
	// Множество абсолютных путей, которые нужно пропустить при сканировании
	// (например, сам выходной файл, чтобы не мержить его повторно).
	excludeSet := make(map[string]bool, len(exclude))
	for _, e := range exclude {
		if abs, err := filepath.Abs(e); err == nil {
			excludeSet[abs] = true
		}
	}

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

		if abs, err := filepath.Abs(path); err == nil && excludeSet[abs] {
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
		effName, generic := c.effectiveName(name, source, fullPath)
		// Если под этим именем уже лежит идентичный кластер — это повторное
		// включение того же самого, переиспользуем без суффикса.
		if existing, ok := c.mergedConfig.Clusters[effName]; ok && cleaner.ClustersEqual(existing, cluster) {
			clusterMapping[name] = effName
			continue
		}
		uniqueName := c.getUniqueName(effName, "cluster", source, fullPath, name, generic)
		c.mergedConfig.Clusters[uniqueName] = cluster
		clusterMapping[name] = uniqueName
	}

	for name, authInfo := range config.AuthInfos {
		effName, generic := c.effectiveName(name, source, fullPath)
		if existing, ok := c.mergedConfig.AuthInfos[effName]; ok && cleaner.UsersEqual(existing, authInfo) {
			userMapping[name] = effName
			continue
		}
		uniqueName := c.getUniqueName(effName, "user", source, fullPath, name, generic)
		c.mergedConfig.AuthInfos[uniqueName] = authInfo
		userMapping[name] = uniqueName
	}

	// Затем обрабатываем контексты, используя уже созданный маппинг
	contextMapping := make(map[string]string)
	for name, context := range config.Contexts {
		newContext := context.DeepCopy()
		// Используем маппинг вместо повторного вызова getUniqueName
		if mappedCluster, ok := clusterMapping[context.Cluster]; ok {
			newContext.Cluster = mappedCluster
		}
		if mappedUser, ok := userMapping[context.AuthInfo]; ok {
			newContext.AuthInfo = mappedUser
		}

		effName, generic := c.effectiveName(name, source, fullPath)
		// Если под этим именем уже лежит контекст с тем же содержимым
		// (кластер/пользователь/namespace) — переиспользуем.
		if existing, ok := c.mergedConfig.Contexts[effName]; ok && contextsSameContent(existing, newContext, c.mergedConfig.Clusters, c.mergedConfig.AuthInfos) {
			contextMapping[name] = effName
			continue
		}

		uniqueName := c.getUniqueName(effName, "context", source, fullPath, name, generic)
		c.mergedConfig.Contexts[uniqueName] = newContext
		contextMapping[name] = uniqueName
	}

	// current-context из исходного файла переносим на итоговое имя контекста;
	// побеждает первый источник, где current-context задан (обычно это база —
	// уже существующий output, обрабатываемый первым), последующие файлы его
	// не перезаписывают.
	if config.CurrentContext != "" && c.mergedConfig.CurrentContext == "" {
		if mapped, ok := contextMapping[config.CurrentContext]; ok {
			c.mergedConfig.CurrentContext = mapped
		}
	}
}

// contextsSameContent сравнивает контексты не по строкам-именам кластера и
// пользователя, а по содержимому, на которое эти имена ссылаются (плюс
// namespace). Так дрейф имени — например, кластер посчитан под чуть другим
// путём, чем в прошлый раз, но сам сервер/сертификат не изменились — не
// считается конфликтом: если контент идентичен, запись не трогаем.
func contextsSameContent(a, b *clientcmdapi.Context, clusters map[string]*clientcmdapi.Cluster, users map[string]*clientcmdapi.AuthInfo) bool {
	if a.Namespace != b.Namespace {
		return false
	}
	return cleaner.ClustersEqual(clusters[a.Cluster], clusters[b.Cluster]) &&
		cleaner.UsersEqual(users[a.AuthInfo], users[b.AuthInfo])
}

// Dedupe схлопывает в итоговом конфиге записи, идентичные по содержимому, но
// оказавшиеся под разными именами (в т.ч. контексты, отличающиеся только
// namespace). Делает результат merge идемпотентным. Возвращает отчёт.
func (c *Composer) Dedupe() *cleaner.DedupeReport {
	deduped, report := cleaner.Dedupe(c.mergedConfig)
	c.mergedConfig = deduped
	return report
}

// getUniqueName подбирает уникальное имя для сущности. name уже прошло через
// effectiveName (типовые имена вроде "default"/"kubernetes-admin" заменены на
// путь к файлу вызывающей стороной). useNumericSuffix — true, если такая
// замена была произведена: тогда при конфликте используется числовой суффикс
// "_N" вместо обычного "-source", т.к. у подставленного имени нет
// "оригинального" короткого варианта, к которому имеет смысл приписывать имя
// файла ещё раз.
func (c *Composer) getUniqueName(name, prefix, source, fullPath, originalItemName string, useNumericSuffix bool) string {
	originalName := name

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

	// Формируем предложение: числовой суффикс для подставленных путём имён,
	// иначе — на основе имени файла.
	var suggestedName string
	if useNumericSuffix {
		suggestedName = fmt.Sprintf("%s_%d", name, counts[name])
	} else {
		suggestedName = fmt.Sprintf("%s-%s", originalItemName, source)
	}

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
			// Цветной вывод: желтый для старого, зеленый для нового
			fmt.Printf("→ Автоматически принято: \033[33m%s\033[0m → \033[32m%s\033[0m\n", name, suggestedName)
			return suggestedName
		}
		// Если занято, используем числовой суффикс
		finalName := numberedName(name, counts[name], useNumericSuffix)
		counts[finalName] = 0
		sources[finalName] = nameSource{
			filePath: fullPath,
			itemType: prefix,
			itemName: originalName,
		}
		fmt.Printf("→ Автоматически принято: \033[33m%s\033[0m → \033[32m%s\033[0m\n", name, finalName)
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
		return numberedName(name, counts[name], useNumericSuffix)
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

// OutputUnchanged сообщает, совпадает ли результат текущего merge с уже
// записанным по пути path файлом по содержимому (имена и наборы
// кластеров/пользователей/контекстов, current-context) — чтобы не
// перезаписывать файл и не создавать бэкап, если по факту ничего не
// изменилось.
func (c *Composer) OutputUnchanged(path string) (bool, error) {
	existing, err := clientcmd.LoadFromFile(path)
	if err != nil {
		return false, fmt.Errorf("failed to load existing config: %w", err)
	}
	return configsEqual(existing, c.mergedConfig), nil
}

// ContextDiff возвращает имена контекстов, которые появятся (added) и
// исчезнут (removed) в результате merge по сравнению с уже записанным по
// пути path файлом — чтобы перед перезаписью явно показать пользователю,
// что именно изменится. Оба среза отсортированы для детерминированного вывода.
func (c *Composer) ContextDiff(path string) (added, removed []string, err error) {
	existing, err := clientcmd.LoadFromFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load existing config: %w", err)
	}
	for name := range c.mergedConfig.Contexts {
		if _, ok := existing.Contexts[name]; !ok {
			added = append(added, name)
		}
	}
	for name := range existing.Contexts {
		if _, ok := c.mergedConfig.Contexts[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed, nil
}

// configsEqual сравнивает два конфига по содержимому: одинаковые имена и
// содержимое кластеров/пользователей (без учёта LocationOfOrigin — он лишь
// отражает, из какого файла объект был загружен, и не относится к
// содержимому), контексты — по ссылкам на кластер/пользователя и namespace,
// плюс current-context.
func configsEqual(a, b *clientcmdapi.Config) bool {
	if a.CurrentContext != b.CurrentContext {
		return false
	}
	if len(a.Clusters) != len(b.Clusters) || len(a.AuthInfos) != len(b.AuthInfos) || len(a.Contexts) != len(b.Contexts) {
		return false
	}
	for name, cluster := range a.Clusters {
		other, ok := b.Clusters[name]
		if !ok || !cleaner.ClustersEqual(cluster, other) {
			return false
		}
	}
	for name, user := range a.AuthInfos {
		other, ok := b.AuthInfos[name]
		if !ok || !cleaner.UsersEqual(user, other) {
			return false
		}
	}
	for name, ctx := range a.Contexts {
		other, ok := b.Contexts[name]
		if !ok || ctx.Cluster != other.Cluster || ctx.AuthInfo != other.AuthInfo || ctx.Namespace != other.Namespace {
			return false
		}
	}
	return true
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

	// Копируем первые увиденные объекты, чтобы отличать реальный конфликт от
	// повторного включения идентичной записи.
	tempClusterObjs := make(map[string]*clientcmdapi.Cluster)
	tempUserObjs := make(map[string]*clientcmdapi.AuthInfo)
	tempContextObjs := make(map[string]*clientcmdapi.Context)
	for k, v := range c.clusterObjs {
		tempClusterObjs[k] = v
	}
	for k, v := range c.userObjs {
		tempUserObjs[k] = v
	}
	for k, v := range c.contextObjs {
		tempContextObjs[k] = v
	}

	// Проверяем контексты и связанные кластеры/пользователей
	for contextName, context := range config.Contexts {
		originalContextName := contextName
		contextName, contextGeneric := c.effectiveName(contextName, source, fullPath)

		clusterName := context.Cluster
		originalClusterName := clusterName
		clusterName, clusterGeneric := c.effectiveName(clusterName, source, fullPath)

		userName := context.AuthInfo
		originalUserName := userName
		userName, userGeneric := c.effectiveName(userName, source, fullPath)

		conflict := &conflictResolution{
			configName: source,
			source:     fullPath,
		}

		hasConflict := false

		clusterObj := config.Clusters[originalClusterName]
		userObj := config.AuthInfos[originalUserName]

		// Проверяем кластер: конфликт только если имя занято ДРУГИМ содержимым.
		if existing, exists := tempClusterObjs[clusterName]; exists {
			if !cleaner.ClustersEqual(existing, clusterObj) {
				tempClusterCounts[clusterName]++
				conflict.clusterOld = clusterName
				conflict.clusterNew = numberedName(clusterName, tempClusterCounts[clusterName], clusterGeneric)
				hasConflict = true
			}
		} else {
			tempClusterCounts[clusterName] = 0
			tempClusterObjs[clusterName] = clusterObj
		}

		// Проверяем пользователя
		if existing, exists := tempUserObjs[userName]; exists {
			if !cleaner.UsersEqual(existing, userObj) {
				tempUserCounts[userName]++
				conflict.userOld = userName
				conflict.userNew = numberedName(userName, tempUserCounts[userName], userGeneric)
				hasConflict = true
			}
		} else {
			tempUserCounts[userName] = 0
			tempUserObjs[userName] = userObj
		}

		// Проверяем контекст: сравниваем по содержимому кластера/пользователя,
		// на которые он ссылается (contextsSameContent), а не по строкам-именам
		// — иначе дрейф имени (например, кластер посчитан под чуть другим
		// путём, чем в прошлый раз) ошибочно считался бы конфликтом, даже если
		// сервер/сертификат не изменились.
		remappedContext := &clientcmdapi.Context{
			Cluster:   clusterName,
			AuthInfo:  userName,
			Namespace: context.Namespace,
		}
		if existing, exists := tempContextObjs[contextName]; exists {
			if !contextsSameContent(existing, remappedContext, tempClusterObjs, tempUserObjs) {
				tempContextCounts[contextName]++
				conflict.contextOld = contextName
				conflict.contextNew = numberedName(contextName, tempContextCounts[contextName], contextGeneric)
				hasConflict = true
			}
		} else {
			tempContextCounts[contextName] = 0
			tempContextObjs[contextName] = remappedContext
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

	// Обновляем реестр увиденных объектов
	c.clusterObjs = tempClusterObjs
	c.userObjs = tempUserObjs
	c.contextObjs = tempContextObjs
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

	// Создаем таблицу с tablewriter
	table := tablewriter.NewWriter(os.Stdout)
	table.SetBorder(true)
	table.SetRowLine(true)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)

	// Добавляем заголовок таблицы
	table.SetHeader([]string{"Конфиг / Тип", "До", "После"})

	// Настраиваем цвета для заголовков и колонок (после SetHeader!)
	table.SetHeaderColor(
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgYellowColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgGreenColor},
	)

	table.SetColumnColor(
		tablewriter.Colors{tablewriter.FgWhiteColor},
		tablewriter.Colors{tablewriter.FgYellowColor},
		tablewriter.Colors{tablewriter.FgGreenColor},
	)

	// Добавляем данные
	for source, conflicts := range conflictsBySource {
		// Добавляем строку с путем к конфигу (выделяем цветом)
		configName := filepath.Base(source)
		table.Append([]string{
			fmt.Sprintf("\033[1;36m%s\033[0m", configName), // Bold Cyan
			"",
			"",
		})

		// Добавляем конфликты
		for _, conflict := range conflicts {
			if conflict.clusterOld != "" {
				table.Append([]string{
					"  cluster",
					conflict.clusterOld,
					conflict.clusterNew,
				})
			}
			if conflict.userOld != "" {
				table.Append([]string{
					"  user",
					conflict.userOld,
					conflict.userNew,
				})
			}
			if conflict.contextOld != "" {
				table.Append([]string{
					"  context",
					conflict.contextOld,
					conflict.contextNew,
				})
			}
		}
	}

	table.Render()
	fmt.Println()

	fmt.Print("Применить эти имена автоматически? [Y/n]: ")

	var response string
	fmt.Scanln(&response)
	response = strings.TrimSpace(strings.ToLower(response))

	return response == "" || response == "y" || response == "yes" || response == "д" || response == "да"
}

func (c *Composer) ShowAppliedChanges() {
	if len(c.conflicts) == 0 {
		return
	}

	fmt.Println()
	pterm.FgGreen.Println("✓ Применённые изменения")
	fmt.Println()

	// Группируем конфликты по источнику
	conflictsBySource := make(map[string][]conflictResolution)
	for _, conflict := range c.conflicts {
		conflictsBySource[conflict.source] = append(conflictsBySource[conflict.source], conflict)
	}

	// Создаем таблицу
	table := tablewriter.NewWriter(os.Stdout)
	table.SetBorder(true)
	table.SetRowLine(true)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)

	// Заголовок
	table.SetHeader([]string{"Конфиг / Тип", "До", "После"})

	// Цвета
	table.SetHeaderColor(
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgYellowColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgGreenColor},
	)

	table.SetColumnColor(
		tablewriter.Colors{tablewriter.FgWhiteColor},
		tablewriter.Colors{tablewriter.FgYellowColor},
		tablewriter.Colors{tablewriter.FgGreenColor},
	)

	// Добавляем данные
	for source, conflicts := range conflictsBySource {
		configName := filepath.Base(source)
		table.Append([]string{
			fmt.Sprintf("\033[1;36m%s\033[0m", configName),
			"",
			"",
		})

		for _, conflict := range conflicts {
			if conflict.clusterOld != "" {
				table.Append([]string{
					"  cluster",
					conflict.clusterOld,
					conflict.clusterNew,
				})
			}
			if conflict.userOld != "" {
				table.Append([]string{
					"  user",
					conflict.userOld,
					conflict.userNew,
				})
			}
			if conflict.contextOld != "" {
				table.Append([]string{
					"  context",
					conflict.contextOld,
					conflict.contextNew,
				})
			}
		}
	}

	table.Render()
	fmt.Println()
}
