# Kubeconfig Composer

CLI-утилита, которая собирает kubeconfig файлы из директории (по умолчанию `~/.kube`) в один файл. Каждая сущность получает уникальное имя, повторный запуск ничего не дублирует. Для готового конфига есть проверка доступности кластеров, очистка недоступных контекстов, дедупликация и откат из бэкапа.

## Установка

Через curl (Linux и macOS, amd64 и arm64). Скрипт приложен к каждому релизу: он скачивает бинарник из GitHub Releases, проверяет sha256 и кладёт его в `~/.local/bin`:

```bash
curl -fsSL https://github.com/Naymi/kubeconfig-composer/releases/latest/download/install.sh | sh
```

Эта ссылка ведёт на последний стабильный релиз. Пока в релизах только alpha-пререлизы, она отвечает 404 (GitHub не считает пререлиз за latest). Тогда возьмите скрипт из нужного релиза по тегу со [страницы Releases](https://github.com/Naymi/kubeconfig-composer/releases) и выберите канал alpha:

```bash
curl -fsSL https://github.com/Naymi/kubeconfig-composer/releases/download/<тег>/install.sh | KC_CHANNEL=alpha sh
```

Те же команды обновляют уже установленную версию; если она актуальна, скрипт ничего не меняет.

| Переменная | Значение |
|---|---|
| `KC_CHANNEL` | `stable` (по умолчанию) или `alpha` |
| `KC_VERSION` | конкретная версия, например `v0.0.2-alpha.1`; приоритетнее канала |
| `KC_INSTALL_DIR` | каталог установки, по умолчанию `~/.local/bin` |

Если `~/.local/bin` нет в `PATH`, скрипт напечатает, что добавить в профиль оболочки. Установленную версию показывает `kubeconfig-composer --version`.

Или через Go:

```bash
go install github.com/naymi/kubeconfig-composer@latest
```

Или соберите из исходников (нужен Go 1.24+):

```bash
git clone https://github.com/naymi/kubeconfig-composer.git
cd kubeconfig-composer
go build -o kubeconfig-composer
```

Бинарники для windows (amd64, arm64) тоже лежат на странице Releases, но скрипт их не ставит: скачайте zip вручную.

## Команды

| Команда | Что делает |
|---|---|
| `merge` | Объединяет kubeconfig файлы в один |
| `list` | Показывает найденные kubeconfig файлы |
| `status` | Проверяет доступность кластеров |
| `cleanup` | Удаляет недоступные контексты |
| `dedupe` | Схлопывает одинаковые по содержимому записи |
| `revert` | Восстанавливает конфиг из бэкапа |

Подробности по любой команде: `kubeconfig-composer <команда> --help`.

### merge: объединение файлов

```bash
# Просканировать ~/.kube и записать результат в ~/.kube/config
kubeconfig-composer merge

# Другая директория
kubeconfig-composer merge --dir /path/to/configs

# Конкретные файлы
kubeconfig-composer merge --files ~/.kube/dev.yaml,~/.kube/prod.yaml

# Другой выходной файл
kubeconfig-composer merge --output ~/.kube/merged-config

# Принять предложенные имена без вопросов
kubeconfig-composer merge -y

# После успешного объединения переместить исходные файлы в trash
kubeconfig-composer merge --cleanup

# Свой префикс имён вместо `kc` (пустая строка отключает префикс)
kubeconfig-composer merge --prefix src
```

### list: просмотр найденных файлов

```bash
kubeconfig-composer list
kubeconfig-composer list --dir /path/to/configs
```

### status: проверка подключения к кластерам

```bash
# Контексты из ~/.kube/config
kubeconfig-composer status

# Конкретный файл
kubeconfig-composer status --kubeconfig ~/.kube/merged-config

# Все kubeconfig файлы в ~/.kube (или в директории из --dir)
kubeconfig-composer status --all

# Таймаут подключения в секундах (по умолчанию 3)
kubeconfig-composer status --timeout 5
```

### cleanup: удаление недоступных контекстов

`cleanup` проверяет кластеры так же, как `status`, и удаляет контексты недоступных кластеров вместе с кластерами и пользователями, на которые больше никто не ссылается. Файл, в котором не осталось ни одного доступного контекста, удаляется целиком. Исключение: `~/.kube/config` не удаляется никогда, из него убираются только контексты.

```bash
kubeconfig-composer cleanup

# Конкретный файл
kubeconfig-composer cleanup --kubeconfig ~/.kube/merged-config

# Все файлы в ~/.kube
kubeconfig-composer cleanup --all

# Показать, что будет удалено, ничего не меняя
kubeconfig-composer cleanup --all --dry-run

# Без подтверждения
kubeconfig-composer cleanup --all --yes
```

### dedupe: схлопывание дубликатов

`dedupe` сравнивает записи по содержимому, а не по имени. Одинаковые кластеры (server и CA) и пользователи (учётные данные) сливаются в одну запись. Контексты с одной парой «кластер + пользователь» тоже сливаются, namespace при этом не различается, остаётся непустой. Именем остаётся самое короткое, а имя из `current-context` всегда сохраняется.

```bash
# ~/.kube/config (или первый путь из $KUBECONFIG)
kubeconfig-composer dedupe

# Только показать изменения
kubeconfig-composer dedupe --dry-run

# Конкретный файл без подтверждения
kubeconfig-composer dedupe -o ~/.kube/config -y
```

### revert: откат из бэкапа

`revert` берёт самый свежий бэкап, который отличается от текущего файла.

```bash
# Восстановить ~/.kube/config
kubeconfig-composer revert

# Показать доступные бэкапы
kubeconfig-composer revert --list

# Восстановить другой файл
kubeconfig-composer revert --output ~/.kube/merged-config
```

## Как работает merge

1. Рекурсивно сканирует директорию (по умолчанию `~/.kube`). Подходит файл с расширением `.yaml` или `.yml`, с именем `config` или `kubeconfig*`, либо файл с содержимым kubeconfig.
2. Если выходной файл уже существует, он загружается первым и служит основой: его имена сохраняются. Сам он при сканировании пропускается.
3. Каждый кластер, пользователь и контекст получает имя `<префикс>:<имя>:<путь от --dir>`. Префикс по умолчанию `kc`. Типовые имена-заглушки (`local`, `user`, `default`, `kubernetes-admin` и подобные) заменяются именем исходного файла.
4. Сущности сравниваются по содержимому, а не по имени: совпавшее содержимое переиспользует существующее имя и не считается конфликтом.
5. Перед записью результат дедуплицируется. Если выходной файл при этом не изменился, запись и подтверждение пропускаются.
6. Существующий выходной файл копируется в `~/.kubeconfig-composer/backups/` как `<имя>.backup.<время>`, после чего записывается новый.

Повторный запуск `merge` на тех же файлах ничего не меняет и не накапливает дубликаты.

### Пример имён

Файлы в `~/.kube`:

- `dev/config` с контекстом `prod`
- `staging.conf` с контекстом `default`

Результат:

- `kc:prod:dev/config`
- `kc:staging:staging.conf`

Если два разных по содержимому объекта получили одно и то же имя (редкий случай, когда у двух файлов совпали и имя, и путь), второй получает числовой суффикс `_N`.

## Рабочие файлы

Утилита хранит своё состояние в `~/.kubeconfig-composer/`:

- `backups/`: копии выходного файла, сделанные перед перезаписью. Отсюда читает `revert`.
- `trash/`: исходные файлы после `merge --cleanup`, под именем `<время>.<имя>`. Файлы перемещаются, а не удаляются.

## Разработка

Все команды сборки и проверки собраны в [Taskfile](https://taskfile.dev):

```bash
task setup   # подготовить checkout: зависимости и pre-commit hook
task build   # собрать бинарник
task test    # тесты
task check   # fmt:check, vet, test
task --list  # все задачи
```

Инструкции для AI-агентов и описание устройства кода: [AGENTS.md](AGENTS.md).
