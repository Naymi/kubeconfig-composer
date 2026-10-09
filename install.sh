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
	path_hint
}

# Вся логика в функции: при обрыве `curl | sh` недокачанный скрипт не выполнится частично.
main "$@"
