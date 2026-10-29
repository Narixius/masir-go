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
#
# The downloads are named masir-desktop-<os>-<version>-<arch>.<ext>, like the
# masir-cli-<os>-<version>-<arch> archives the release also publishes.
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

# Verify the geo files against the source sums on every build, not just when they are
# missing: a previous interrupted download could have left a truncated file. Set
# MASIR_GEO_SKIP=1 for an offline build that already has its files.
if [ "${MASIR_GEO_SKIP:-0}" != 1 ]; then
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

# MyGo has the platform fix the app needs, a patch in patches/: its macOS popover animates
# a content size change on AppKit's own schedule, so the panel's height ran ahead of the
# lists it holds. Build against a patched copy, and put the module files back after. The
# Windows resize fixes are upstream since MyGo 0.7.3 and need no patch.
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

# go list -m reports the module's directory only when its source is already in the module
# cache; it downloads nothing. A fresh runner (or one whose test job failed before saving
# its cache) has no MyGo there, so ask for the download explicitly.
mygo_dir=$(cd apps/masir-gui && go mod download -json github.com/egoist/mygo |
	sed -n 's/.*"Dir": "\(.*\)".*/\1/p')
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

# Every download is named <app>-<role>-<os>-<version>-<arch>: `portable` is the archive a
# person unpacks and runs, `update` is the one MyGo's updater downloads, and the setup
# downloads name their format (the .dmg, the .exe, the .deb). MyGo names what it builds
# after mygo.json (masir-desktop-<version>-<arch>.dmg, masir-desktop_<version>_<arch>.deb,
# masir-desktop-<version>-<platform>.tar.gz), so each app build is renamed here: one place
# owns the names, and `make gui` produces the files the release workflow uploads.
expected() {
	[ -f "$1" ] || {
		echo "build-gui: mygo built no $(basename -- "$1")" >&2
		exit 1
	}
}

name_app() {
	app_os=$1
	app_arch=$2
	app_dir="apps/masir-gui/dist/$app_os-$app_arch"
	portable="$app_dir/masir-desktop-portable-$app_os-$version-$app_arch.tar.gz"

	case "$app_os" in
	darwin)
		expected "$app_dir/masir-desktop-$version-$app_arch.dmg"
		mv -f "$app_dir/masir-desktop-$version-$app_arch.dmg" \
			"$app_dir/masir-desktop-darwin-$version-$app_arch.dmg"
		;;
	windows)
		expected "$app_dir/masir-desktop-setup-$version-$app_arch.exe"
		mv -f "$app_dir/masir-desktop-setup-$version-$app_arch.exe" \
			"$app_dir/masir-desktop-setup-windows-$version-$app_arch.exe"
		# Windows is unpacked from the .zip the release workflow packs, not from a
		# tarball: its tarball exists only for MyGo's updater.
		portable=""
		;;
	linux)
		expected "$app_dir/masir-desktop_${version}_${app_arch}.deb"
		mv -f "$app_dir/masir-desktop_${version}_${app_arch}.deb" \
			"$app_dir/masir-desktop-linux-$version-$app_arch.deb"
		# install.sh, which sits beside the archives, looks for the portable one under
		# MyGo's name; teach it the name it is next to now, and the one the usage text
		# shows.
		expected "$app_dir/install.sh"
		sed -i.bak 's/\$name-\$version-\$target\.tar\.gz/masir-desktop-portable-${target%%-*}-$version-${target##*-}.tar.gz/g' \
			"$app_dir/install.sh"
		sed -i.bak 's|\$name-\$version-<platform>\.tar\.gz|masir-desktop-portable-<os>-<version>-<arch>.tar.gz|g' \
			"$app_dir/install.sh"
		sed -i.bak 's|download \$name-<version>-\$target\.tar\.gz|download masir-desktop-portable-<os>-<version>-<arch>.tar.gz|g' \
			"$app_dir/install.sh"
		rm -f "$app_dir/install.sh.bak"
		;;
	esac

	# MyGo's update archive and manifest use MyGo's own name. Rename the archive to the
	# one every download uses and point the manifest at it; MyGo's updater reads the URL
	# from the manifest, so the name is ours to choose. On macOS and Linux the portable
	# tarball is also the one the updater reads: same bytes, one download.
	platform="$app_os-$app_arch"
	old_archive="$app_dir/masir-desktop-$version-$platform.tar.gz"
	manifest="$app_dir/update-$platform.json"
	new_archive="$portable"
	if [ -z "$new_archive" ]; then
		new_archive="$app_dir/masir-desktop-update-$app_os-$version-$app_arch.tar.gz"
	fi
	if [ -f "$manifest" ]; then
		expected "$old_archive"
		mv -f "$old_archive" "$new_archive"
		sed -i.bak "s|$(basename -- "$old_archive")|$(basename -- "$new_archive")|g" "$manifest"
		rm -f "$manifest.bak"
	fi
}

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
		name_app "$os" "$arch"
		built="$built $os/$arch"
	done
done

printf '\nBuilt into apps/masir-gui/dist:%s\n' "$built"
