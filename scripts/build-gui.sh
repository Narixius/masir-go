#!/bin/sh
# Builds the desktop app, with the masir it ships, for the systems and architectures chosen.
#
#   sh scripts/build-gui.sh                                   # all: 3 systems x 2 architectures
#   GUI_OS=windows GUI_ARCHES=arm64 sh scripts/build-gui.sh   # one build
#   GUI_OS="darwin linux" GUI_ARCHES=amd64 sh scripts/build-gui.sh
#
# GUI_OS (darwin, linux, windows) and GUI_ARCHES (amd64, arm64) are space or comma
# separated. Each build goes to apps/masir-gui/dist/<os>-<arch>, with the masir executable
# and the geo data in the bin folder of its resources. The version is mygo.json's, so the
# app and the masir it ships have the same one.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

os_list=$(printf '%s' "${GUI_OS:-darwin linux windows}" | tr ',' ' ' | tr -d '\r')
arch_list=$(printf '%s' "${GUI_ARCHES:-amd64 arm64}" | tr ',' ' ' | tr -d '\r')
[ -n "$os_list" ] || { echo "build-gui: GUI_OS is empty" >&2; exit 1; }
[ -n "$arch_list" ] || { echo "build-gui: GUI_ARCHES is empty" >&2; exit 1; }

version=${GUI_VERSION:-$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' apps/masir-gui/mygo.json)}
version=$(printf '%s' "$version" | tr -d '\r')
[ -n "$version" ] || { echo "build-gui: no version in apps/masir-gui/mygo.json" >&2; exit 1; }

if [ ! -f build/geo/geoip.dat ] || [ ! -f build/geo/geosite.dat ]; then
	sh scripts/fetch-geo.sh
fi

# The same variables the Makefile's `build` links: analytics and update signing, when
# they are set, and the version.
ldflags=
add() {
	if [ -n "${2:-}" ]; then
		ldflags="$ldflags -X $1=$2"
	fi
}
add github.com/narixius/masir-go/libs/masir-daemon/internal/analytics.apiKey "${MASIR_POSTHOG_KEY:-}"
add github.com/narixius/masir-go/libs/masir-daemon/internal/analytics.host "${MASIR_POSTHOG_HOST:-}"
add github.com/narixius/masir-go/libs/masir-daemon/internal/update.publicKeys "${MASIR_UPDATE_PUBKEY:-}"
ldflags="$ldflags -X main.version=$version"

# MyGo has two platform fixes the app needs, each a patch in patches/: its Windows swap
# chain stretched a frame drawn for one client height into a window that changed height,
# magnifying the content for a moment on every resize, and its macOS popover animates a
# content size change on AppKit's own schedule, so the panel's height ran ahead of the
# lists it holds. Build against a patched copy, and put the module files back after.
work="$root/.mygo-patched"
go_mod="$root/apps/masir-gui/go.mod"
go_sum="$root/apps/masir-gui/go.sum"
go_mod_orig="$root/.mygo-patched.go.mod"
go_sum_orig="$root/.mygo-patched.go.sum"
cleanup() {
	if [ -f "$go_mod_orig" ]; then
		cp "$go_mod_orig" "$go_mod"
		rm -f "$go_mod_orig"
	fi
	if [ -f "$go_sum_orig" ]; then
		cp "$go_sum_orig" "$go_sum"
		rm -f "$go_sum_orig"
	fi
	rm -rf "$work"
}
trap cleanup EXIT INT TERM

mygo_dir=$(cd apps/masir-gui && go list -m -f '{{.Dir}}' github.com/egoist/mygo)
if [ -z "$mygo_dir" ] || [ ! -d "$mygo_dir" ]; then
	echo "build-gui: cannot find the MyGo module source" >&2
	exit 1
fi
rm -rf "$work"
cp -R "$mygo_dir" "$work"
chmod -R u+w "$work"
printf 'Patching MyGo for the resizes...\n'
for patch in "$root"/patches/mygo-*.patch; do
	if ! git apply -p1 --directory=.mygo-patched --whitespace=nowarn "$patch"; then
		echo "build-gui: $(basename "$patch") does not apply to mygo $(cd apps/masir-gui && go list -m -f '{{.Version}}' github.com/egoist/mygo)" >&2
		exit 1
	fi
done
cp "$go_mod" "$go_mod_orig"
cp "$go_sum" "$go_sum_orig"
(cd apps/masir-gui && go mod edit -replace github.com/egoist/mygo=../../.mygo-patched)

built=
for os in $os_list; do
	for arch in $arch_list; do
		printf '==> %s/%s (Masir %s)\n' "$os" "$arch" "$version"
		res="apps/masir-gui/resources/$os-$arch/bin"
		name=masir
		if [ "$os" = windows ]; then
			name=masir.exe
		fi
		mkdir -p "$res"
		GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -ldflags "$ldflags" -o "$res/$name" ./apps/masir
		cp build/geo/geoip.dat build/geo/geosite.dat "$res/"
		(cd apps/masir-gui && go tool mygo build -platform "$os/$arch")
		built="$built $os/$arch"
	done
done

printf '\nBuilt into apps/masir-gui/dist:%s\n' "$built"
