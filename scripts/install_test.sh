#!/bin/sh
# Тесты install.sh без сети: GitHub подменяется локальным HTTP-сервером
# (KC_API_URL / KC_DOWNLOAD_URL). Запуск: task test:install
set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
INSTALL_SH="$ROOT/install.sh"
WORK="$ROOT/.tmp/install-test"

rm -rf "$WORK"
mkdir -p "$WORK"

SERVER_PID=""
stop_server() {
	if [ -n "$SERVER_PID" ]; then
		kill "$SERVER_PID" 2>/dev/null || true
		wait "$SERVER_PID" 2>/dev/null || true
		SERVER_PID=""
	fi
}
trap 'stop_server; chmod -R u+w "$WORK" 2>/dev/null || true; rm -rf "$WORK"' EXIT

FAILED=0
CASE=""

CASE_FAILED=0
pass() {
	[ "$CASE_FAILED" = 1 ] || printf 'ok   %s\n' "$CASE"
}
fail() {
	printf 'FAIL %s: %s\n' "$CASE" "$1"
	FAILED=1
	CASE_FAILED=1
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

host_os() {
	case "$(uname -s)" in
	Linux) echo linux ;;
	Darwin) echo darwin ;;
	esac
}

host_arch() {
	case "$(uname -m)" in
	x86_64 | amd64) echo amd64 ;;
	arm64 | aarch64) echo arm64 ;;
	esac
}

# new_case <name>: чистая песочница со своим docroot и каталогом установки
new_case() {
	CASE="$1"
	CASE_FAILED=0
	stop_server
	DOCROOT="$WORK/$CASE/docroot"
	INSTALL_DIR="$WORK/$CASE/bin"
	mkdir -p "$DOCROOT/api/releases" "$DOCROOT/dl"
}

# make_release <tag> [corrupt]: архив + checksums.txt, как у GoReleaser
make_release() {
	tag="$1"
	version="${tag#v}"
	name="kubeconfig-composer_${version}_$(host_os)_$(host_arch).tar.gz"
	stage="$WORK/$CASE/stage-$version"
	mkdir -p "$stage" "$DOCROOT/dl/$tag"
	printf '#!/bin/sh\necho "kubeconfig-composer version %s"\n' "$version" >"$stage/kubeconfig-composer"
	chmod +x "$stage/kubeconfig-composer"
	tar -czf "$DOCROOT/dl/$tag/$name" -C "$stage" kubeconfig-composer
	sum="$(sha256_of "$DOCROOT/dl/$tag/$name")"
	if [ "${2:-}" = corrupt ]; then
		sum="0000000000000000000000000000000000000000000000000000000000000000"
	fi
	printf '%s  %s\n' "$sum" "$name" >"$DOCROOT/dl/$tag/checksums.txt"
}

# set_latest <tag>: ответ releases/latest (стабильный релиз)
set_latest() {
	printf '{"tag_name": "%s", "prerelease": false}\n' "$1" >"$DOCROOT/api/releases/latest"
}

# set_list <tag>...: ответ releases?per_page=1 (самый свежий первым)
set_list() {
	{
		printf '[\n'
		first=1
		for t in "$@"; do
			[ "$first" = 1 ] || printf ',\n'
			printf '{"tag_name": "%s"}' "$t"
			first=0
		done
		printf '\n]\n'
	} >"$DOCROOT/api/releases/index.html"
}

start_server() {
	log="$WORK/$CASE/server.log"
	python3 -u -m http.server 0 --bind 127.0.0.1 --directory "$DOCROOT" >"$log" 2>&1 &
	SERVER_PID=$!
	i=0
	PORT=""
	while [ -z "$PORT" ] && [ "$i" -lt 50 ]; do
		PORT="$(sed -n 's/.*port \([0-9][0-9]*\).*/\1/p' "$log" | head -n 1)"
		[ -n "$PORT" ] || sleep 0.1
		i=$((i + 1))
	done
	[ -n "$PORT" ] || {
		echo "сервер не запустился" >&2
		exit 1
	}
}

# run_install [VAR=value ...]: запускает install.sh, вывод в $OUT, код в $RC
run_install() {
	OUT="$WORK/$CASE/out.txt"
	RC=0
	env \
		KC_API_URL="http://127.0.0.1:$PORT/api" \
		KC_DOWNLOAD_URL="http://127.0.0.1:$PORT/dl" \
		KC_INSTALL_DIR="$INSTALL_DIR" \
		"$@" \
		sh "$INSTALL_SH" >"$OUT" 2>&1 || RC=$?
}

installed_version() {
	"$INSTALL_DIR/kubeconfig-composer" --version
}

assert_rc() {
	[ "$RC" = "$1" ] || fail "код выхода $RC, ожидался $1: $(cat "$OUT")"
}

assert_out_contains() {
	grep -qF -- "$1" "$OUT" || fail "в выводе нет «$1»: $(cat "$OUT")"
}

assert_installed() {
	[ "$(installed_version)" = "kubeconfig-composer version $1" ] ||
		fail "установлена версия «$(installed_version 2>&1)», ожидалась $1"
}

assert_not_installed() {
	[ ! -e "$INSTALL_DIR/kubeconfig-composer" ] || fail "бинарник не должен быть установлен"
}

# --- кейсы ---

new_case stable-default
make_release v1.0.0
set_latest v1.0.0
start_server
run_install
assert_rc 0
assert_installed 1.0.0
pass

new_case rerun-is-noop
make_release v1.0.0
set_latest v1.0.0
start_server
run_install
before="$(ls -i "$INSTALL_DIR/kubeconfig-composer")"
run_install
assert_rc 0
assert_out_contains "уже актуальна"
[ "$(ls -i "$INSTALL_DIR/kubeconfig-composer")" = "$before" ] || fail "бинарник перезаписан"
pass

new_case update-to-newer
make_release v1.0.0
make_release v1.1.0
set_latest v1.0.0
start_server
run_install
assert_installed 1.0.0
set_latest v1.1.0
run_install
assert_rc 0
assert_installed 1.1.0
pass

new_case no-stable-releases
make_release v0.1.0-alpha.1
set_list v0.1.0-alpha.1
start_server
run_install
assert_rc 1
assert_out_contains "KC_CHANNEL=alpha"
assert_not_installed
pass

new_case alpha-channel
make_release v0.1.0-alpha.2
make_release v0.1.0-alpha.1
set_list v0.1.0-alpha.2 v0.1.0-alpha.1
start_server
run_install KC_CHANNEL=alpha
assert_rc 0
assert_installed 0.1.0-alpha.2
pass

new_case unknown-channel
start_server
run_install KC_CHANNEL=nightly
assert_rc 1
assert_out_contains "KC_CHANNEL"
pass

new_case pinned-version
make_release v1.0.0
make_release v1.1.0
set_latest v1.1.0
start_server
run_install KC_VERSION=v1.0.0
assert_rc 0
assert_installed 1.0.0
pass

new_case pinned-version-without-v
make_release v1.0.0
start_server
run_install KC_VERSION=1.0.0
assert_rc 0
assert_installed 1.0.0
pass

new_case bad-checksum
make_release v1.0.0 corrupt
set_latest v1.0.0
start_server
run_install
assert_rc 1
assert_out_contains "контрольная сумма"
assert_not_installed
pass

new_case missing-release-asset
set_latest v9.9.9
start_server
run_install
assert_rc 1
assert_not_installed
pass

new_case unsupported-os
fakebin="$WORK/$CASE/fakebin"
mkdir -p "$fakebin"
printf '#!/bin/sh\necho FreeBSD\n' >"$fakebin/uname"
chmod +x "$fakebin/uname"
start_server
run_install PATH="$fakebin:$PATH"
assert_rc 1
assert_out_contains "FreeBSD"
pass

new_case unwritable-install-dir
make_release v1.0.0
set_latest v1.0.0
mkdir -p "$INSTALL_DIR"
chmod 555 "$INSTALL_DIR"
start_server
run_install
chmod 755 "$INSTALL_DIR"
assert_rc 1
assert_out_contains "KC_INSTALL_DIR"
assert_not_installed
pass

new_case creates-install-dir
make_release v1.0.0
set_latest v1.0.0
start_server
[ ! -e "$INSTALL_DIR" ] || fail "каталог не должен существовать до установки"
run_install KC_INSTALL_DIR="$INSTALL_DIR/nested/bin"
assert_rc 0
[ -x "$INSTALL_DIR/nested/bin/kubeconfig-composer" ] || fail "бинарник не создан во вложенном каталоге"
pass

stop_server
if [ "$FAILED" = 0 ]; then
	echo "все тесты install.sh прошли"
else
	echo "есть упавшие тесты install.sh" >&2
	exit 1
fi
