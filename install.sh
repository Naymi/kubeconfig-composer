#!/bin/sh
# Установка и обновление kubeconfig-composer из GitHub Releases.
#
#   curl -fsSL https://github.com/Naymi/kubeconfig-composer/releases/latest/download/install.sh | sh
#
# Повторный запуск обновляет бинарник до самой свежей версии выбранного канала.
#
# Переменные окружения:
#   KC_CHANNEL      stable (по умолчанию) или alpha: последний релиз, включая пререлизы
#   KC_VERSION      конкретный тег (v0.0.2-alpha.1 или 0.0.2-alpha.1), имеет приоритет над каналом
#   KC_INSTALL_DIR  каталог установки (по умолчанию ~/.local/bin)
#   KC_SHELL_INIT   1 — подключить init в профиле оболочки: в rc-файл дописывается
#                   блок с PATH и eval "$(kubeconfig-composer init)"; повторный запуск
#                   заменяет блок, а не добавляет второй
#   KC_SHELL_RC     какой rc-файл править; по умолчанию по $SHELL:
#                   zsh — ${ZDOTDIR:-~}/.zshrc, bash — ~/.bashrc
#   KC_API_URL      база GitHub API; для тестов
#   KC_DOWNLOAD_URL база скачивания релизов; для тестов

set -eu

REPO="Naymi/kubeconfig-composer"
BINARY="kubeconfig-composer"

API_URL="${KC_API_URL:-https://api.github.com/repos/$REPO}"
DOWNLOAD_URL="${KC_DOWNLOAD_URL:-https://github.com/$REPO/releases/download}"
INSTALL_DIR="${KC_INSTALL_DIR:-$HOME/.local/bin}"
CHANNEL="${KC_CHANNEL:-stable}"

TMP_DIR=""
SHELL_INIT_DONE=""

die() {
	printf 'ошибка: %s\n' "$*" >&2
	exit 1
}

info() {
	printf '%s\n' "$*"
}

cleanup() {
	if [ -n "$TMP_DIR" ]; then
		rm -rf "$TMP_DIR"
	fi
}

need() {
	command -v "$1" >/dev/null 2>&1 || die "не найдена команда $1"
}

detect_os() {
	uname_s="$(uname -s)"
	case "$uname_s" in
	Linux) echo linux ;;
	Darwin) echo darwin ;;
	*) die "система $uname_s не поддерживается (нужен Linux или macOS)" ;;
	esac
}

detect_arch() {
	uname_m="$(uname -m)"
	case "$uname_m" in
	x86_64 | amd64) echo amd64 ;;
	arm64 | aarch64) echo arm64 ;;
	*) die "архитектура $uname_m не поддерживается (нужна amd64 или arm64)" ;;
	esac
}

# http_get <url> <файл>: печатает HTTP-код, не падает на 4xx/5xx
http_get() {
	curl -sSL -o "$2" -w '%{http_code}' "$1" || die "не удалось подключиться к $1"
}

# first_tag: достаёт первый tag_name из JSON; работает и с компактным, и с форматированным ответом
first_tag() {
	grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' "$1" | head -n 1 | sed 's/.*:[[:space:]]*"\(.*\)"/\1/'
}

resolve_tag() {
	if [ -n "${KC_VERSION:-}" ]; then
		case "$KC_VERSION" in
		v*) echo "$KC_VERSION" ;;
		*) echo "v$KC_VERSION" ;;
		esac
		return
	fi

	case "$CHANNEL" in
	stable) api_path="releases/latest" ;;
	alpha) api_path="releases?per_page=1" ;;
	*) die "неизвестный KC_CHANNEL=$CHANNEL (допустимо: stable, alpha)" ;;
	esac

	body="$TMP_DIR/release.json"
	code="$(http_get "$API_URL/$api_path" "$body")"
	case "$code" in
	200) ;;
	404)
		if [ "$CHANNEL" = stable ]; then
			die "стабильных релизов пока нет; установите alpha: KC_CHANNEL=alpha"
		fi
		die "релизы не найдены ($API_URL/$api_path)"
		;;
	403 | 429) die "GitHub API ограничил запросы (HTTP $code); повторите позже или задайте KC_VERSION" ;;
	*) die "GitHub API вернул HTTP $code для $API_URL/$api_path" ;;
	esac

	tag="$(first_tag "$body")"
	[ -n "$tag" ] || die "не удалось определить версию из ответа GitHub API"
	echo "$tag"
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		die "не найдена sha256sum или shasum для проверки контрольной суммы"
	fi
}

download() {
	code="$(http_get "$1" "$2")"
	[ "$code" = 200 ] || die "не удалось скачать $1 (HTTP $code)"
}

installed_version() {
	target="$INSTALL_DIR/$BINARY"
	[ -x "$target" ] || return 0
	"$target" --version 2>/dev/null | awk 'NR == 1 { print $NF }' || true
}

path_hint() {
	case ":$PATH:" in
	*":$INSTALL_DIR:"*) ;;
	*)
		info ""
		info "Каталог $INSTALL_DIR не входит в PATH. Добавьте в профиль оболочки:"
		info "  export PATH=\"$INSTALL_DIR:\$PATH\""
		;;
	esac
}

# sq <строка>: строка в одинарных кавычках для sh, с экранированием '
sq() {
	printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"
}

BLOCK_BEGIN="# >>> kubeconfig-composer >>>"
BLOCK_END="# <<< kubeconfig-composer <<<"

# shell_rc: rc-файл для блока init; пусто, если оболочку не распознали
shell_rc() {
	if [ -n "${KC_SHELL_RC:-}" ]; then
		echo "$KC_SHELL_RC"
		return
	fi
	case "${SHELL:-}" in
	*/zsh) echo "${ZDOTDIR:-$HOME}/.zshrc" ;;
	*/bash) echo "$HOME/.bashrc" ;;
	esac
}

# shell_block: блок для rc-файла. Бинарник вызывается по абсолютному пути,
# чтобы init работал, даже если PATH настроен ниже по файлу. $PATH и $(...)
# попадают в rc-файл как есть — их раскроет оболочка при старте.
# shellcheck disable=SC2016
shell_block() {
	dir="$(sq "$INSTALL_DIR")"
	printf '%s\n' "$BLOCK_BEGIN"
	printf 'case ":$PATH:" in *:%s:*) ;; *) export PATH=%s:"$PATH" ;; esac\n' "$dir" "$dir"
	printf 'eval "$(%s init)"\n' "$(sq "$INSTALL_DIR/$BINARY")"
	printf '%s\n' "$BLOCK_END"
}

# setup_shell_init: при KC_SHELL_INIT=1 вписывает блок в rc-файл (заменяя
# прежний) и ставит SHELL_INIT_DONE=1, иначе печатает подсказку. Пишет через `cat >`, поэтому rc-файл,
# который является символической ссылкой (dotfiles), остаётся ссылкой.
setup_shell_init() {
	if [ "${KC_SHELL_INIT:-}" != 1 ]; then
		info ""
		info "Чтобы KUBECONFIG собирался при старте оболочки, перезапустите установку с KC_SHELL_INIT=1"
		info "или добавьте в профиль: eval \"\$($(sq "$INSTALL_DIR/$BINARY") init)\""
		return 0
	fi

	rc="$(shell_rc)"
	if [ -z "$rc" ]; then
		info ""
		info "Не знаю, какой rc-файл у оболочки ${SHELL:-?}; задайте его через KC_SHELL_RC=<файл>"
		return 0
	fi

	mkdir -p "$(dirname "$rc")"
	new="$TMP_DIR/rc"
	shell_block >"$TMP_DIR/block"
	if [ -f "$rc" ] && grep -qxF "$BLOCK_BEGIN" "$rc"; then
		awk -v begin="$BLOCK_BEGIN" -v end="$BLOCK_END" -v blockfile="$TMP_DIR/block" '
			$0 == begin { while ((getline line < blockfile) > 0) print line; skip = 1; next }
			skip && $0 == end { skip = 0; next }
			!skip { print }
		' "$rc" >"$new"
		action="Обновлён"
	else
		if [ -s "$rc" ]; then
			cat "$rc" >"$new"
			# Отделяем блок пустой строкой; дописываем перевод строки, если его нет в конце.
			[ "$(tail -c 1 "$rc")" = "" ] || printf '\n' >>"$new"
			printf '\n' >>"$new"
		else
			: >"$new"
		fi
		cat "$TMP_DIR/block" >>"$new"
		action="Добавлен"
	fi
	cat "$new" >"$rc"
	SHELL_INIT_DONE=1
	info ""
	info "$action блок kubeconfig-composer в $rc; откройте новый терминал"
}

main() {
	need curl
	need tar
	need uname
	need mktemp

	os="$(detect_os)"
	arch="$(detect_arch)"

	TMP_DIR="$(mktemp -d)"
	trap cleanup EXIT INT TERM

	tag="$(resolve_tag)"
	version="${tag#v}"

	current="$(installed_version)"
	if [ "$current" = "$version" ]; then
		info "$BINARY $version уже актуальна ($INSTALL_DIR/$BINARY)"
		setup_shell_init
		return 0
	fi

	archive="${BINARY}_${version}_${os}_${arch}.tar.gz"
	info "Скачиваю $BINARY $version ($os/$arch)..."
	download "$DOWNLOAD_URL/$tag/$archive" "$TMP_DIR/$archive"
	download "$DOWNLOAD_URL/$tag/checksums.txt" "$TMP_DIR/checksums.txt"

	expected="$(awk -v f="$archive" '$2 == f { print $1 }' "$TMP_DIR/checksums.txt")"
	[ -n "$expected" ] || die "в checksums.txt нет записи для $archive"
	actual="$(sha256_of "$TMP_DIR/$archive")"
	[ "$expected" = "$actual" ] || die "контрольная сумма $archive не совпала (ожидалась $expected, получена $actual)"

	mkdir -p "$TMP_DIR/extract"
	tar -xzf "$TMP_DIR/$archive" -C "$TMP_DIR/extract" "$BINARY" || die "в архиве $archive нет $BINARY"

	mkdir -p "$INSTALL_DIR" 2>/dev/null || true
	[ -d "$INSTALL_DIR" ] && [ -w "$INSTALL_DIR" ] ||
		die "нет прав на запись в $INSTALL_DIR; задайте другой каталог через KC_INSTALL_DIR"

	# Сначала временный файл в каталоге установки, потом атомарный mv: так можно
	# обновить бинарник, который сейчас запущен.
	staged="$INSTALL_DIR/.$BINARY.new.$$"
	cp "$TMP_DIR/extract/$BINARY" "$staged"
	chmod 755 "$staged"
	mv -f "$staged" "$INSTALL_DIR/$BINARY"

	if [ -n "$current" ]; then
		info "Обновлено: $current -> $version ($INSTALL_DIR/$BINARY)"
	else
		info "Установлено: $BINARY $version ($INSTALL_DIR/$BINARY)"
	fi
	setup_shell_init
	# Блок в rc-файле сам добавляет каталог в PATH; без него — подсказка.
	[ "$SHELL_INIT_DONE" = 1 ] || path_hint
}

# Вся логика в функции: при обрыве `curl | sh` недокачанный скрипт не выполнится частично.
main "$@"
