# Kubeconfig Composer

Утилита для объединения множественных kubeconfig файлов из директории `~/.kube` с автоматическим разрешением конфликтов имён контекстов.

## Установка

```bash
go install github.com/naymi/kubeconfig-composer@latest
```

Или клонируйте репозиторий и соберите:

```bash
git clone https://github.com/naymi/kubeconfig-composer.git
cd kubeconfig-composer
go build -o kubeconfig-composer
```

## Использование

### Объединение kubeconfig файлов

```bash
# Сканировать ~/.kube и создать merged-kubeconfig.yaml
kubeconfig-composer merge

# Указать другую директорию
kubeconfig-composer merge --dir /path/to/configs

# Указать конкретные файлы конфигурации
kubeconfig-composer merge --files ~/.kube/config,~/.kube/dev-config.yaml,~/.kube/prod-config.yaml

# Указать выходной файл
kubeconfig-composer merge --output /path/to/output.yaml

# Все параметры вместе
kubeconfig-composer merge -f ~/.kube/config,~/.kube/dev.yaml -o ~/.kube/merged-config
```

### Просмотр найденных файлов

```bash
# Показать все kubeconfig файлы в ~/.kube
kubeconfig-composer list

# Показать файлы в другой директории
kubeconfig-composer list --dir /path/to/configs
```

### Проверка статуса подключения к кластерам

```bash
# Проверить статус для ~/.kube/config
kubeconfig-composer status

# Проверить статус для конкретного файла
kubeconfig-composer status --kubeconfig ~/.kube/merged-config.yaml

# Проверить все kubeconfig файлы в ~/.kube
kubeconfig-composer status --all

# Проверить все kubeconfig файлы в другой директории
kubeconfig-composer status --all --dir /path/to/configs

# Установить таймаут подключения (в секундах)
kubeconfig-composer status --timeout 5
```

### Очистка недоступных контекстов

```bash
# Удалить недоступные контексты из ~/.kube/config
kubeconfig-composer cleanup

# Очистить конкретный файл
kubeconfig-composer cleanup --kubeconfig ~/.kube/merged-config.yaml

# Очистить все kubeconfig файлы в ~/.kube
kubeconfig-composer cleanup --all

# Предварительный просмотр без удаления (dry-run)
kubeconfig-composer cleanup --all --dry-run

# Без подтверждения
kubeconfig-composer cleanup --all --yes

# Установить таймаут подключения
kubeconfig-composer cleanup --all --timeout 5
```

## Как это работает

1. Рекурсивно сканирует указанную директорию (по умолчанию `~/.kube`)
2. Находит все kubeconfig файлы (по расширению `.yaml`, `.yml`, имени `config` или `kubeconfig*`)
3. Парсит каждый файл и извлекает контексты, кластеры и пользователей
4. При конфликте имён автоматически добавляет суффикс `-N`
5. Генерирует объединённый kubeconfig файл

## Примеры

Если у вас есть файлы:
- `~/.kube/config` с контекстом `prod`
- `~/.kube/dev/config` с контекстом `prod`

Результат будет содержать:
- `prod` (из первого файла)
- `prod-1` (из второго файла)
