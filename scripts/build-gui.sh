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
