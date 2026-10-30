#!/bin/sh
# Builds masir-desktop's RPM from the portable Linux archive `make gui` produced.
#
#   sh scripts/build-rpm.sh        # newest archive in apps/masir-gui/dist/linux-<arch>
#   sh scripts/build-rpm.sh path/to/masir-desktop-portable-linux-<version>-<arch>.tar.gz
#
# The .rpm is named like every other release download (see apps/masir-gui/README.md)
# and written beside the archive. rpmbuild runs with --nodeps: its dependency check
# only knows the local rpmdb, which is empty on a non-rpm distro or a CI runner even
# when the tools are installed — the one tool this build actually needs is checked
# here instead, with a message that says what to install.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

case "$(uname -m)" in
	x86_64) host_arch=amd64 ;;
	arm64 | aarch64) host_arch=arm64 ;;
	*) echo "build-rpm: unsupported machine: $(uname -m)" >&2; exit 1 ;;
esac

tarball=${1:-}
if [ -z "$tarball" ]; then
	# Newest wins: rebuilding the app shouldn't force deleting the older archive first.
	tarball=$(ls -t apps/masir-gui/dist/linux-$host_arch/masir-desktop-portable-linux-*.tar.gz 2>/dev/null | head -n 1 || true)
fi
if [ -z "$tarball" ] || [ ! -f "$tarball" ]; then
	echo "build-rpm: no portable archive found." >&2
	echo "  build one first:  make gui GUI_OS=linux   (or pass the archive's path)" >&2
	exit 1
fi

# masir-desktop-portable-linux-<version>-<arch>.tar.gz: pull both out of the name, so
# the package's version and the archive's can't drift apart. A renamed archive is a
# wrong archive, and saying so beats packaging 0.6.5's bytes as 0.6.6.
base=$(basename "$tarball")
version=$(printf '%s' "$base" | sed -n 's/^masir-desktop-portable-linux-\([0-9][0-9.]*\)-\(amd64\|arm64\)\.tar\.gz$/\1/p')
arch=$(printf '%s' "$base" | sed -n 's/^masir-desktop-portable-linux-[0-9][0-9.]*-\(amd64\|arm64\)\.tar\.gz$/\1/p')
if [ -z "$version" ]; then
	echo "build-rpm: $base is not masir-desktop-portable-linux-<version>-<arch>.tar.gz" >&2
	exit 1
fi

# RPM's own names for the two architectures the releases use.
case "$arch" in
	amd64) rpm_target=x86_64 ;;
	arm64) rpm_target=aarch64 ;;
esac

command -v rpmbuild >/dev/null 2>&1 || {
	echo "build-rpm: rpmbuild not found (sudo dnf install rpm-build)" >&2; exit 1; }
command -v convert >/dev/null 2>&1 || {
	echo "build-rpm: ImageMagick not found (sudo dnf install ImageMagick)" >&2; exit 1; }

top=$(mktemp -d)
trap 'rm -rf "$top"' EXIT INT TERM
mkdir -p "$top/SOURCES" "$top/SPECS" "$top/BUILD" "$top/RPMS" "$top/SRPMS"
cp "$tarball" "$top/SOURCES/"

rpmbuild -ba --nodeps \
	--define "_topdir $top" \
	--define "masir_version $version" \
	--define "mygo_arch $arch" \
	--target "$rpm_target" \
	packaging/masir-desktop.spec

rpm_out=$(ls "$top"/RPMS/*/masir-desktop-"$version"-*.rpm 2>/dev/null | head -n 1 || true)
if [ -z "$rpm_out" ]; then
	echo "build-rpm: rpmbuild finished but produced no package" >&2
	exit 1
fi

dest=$(dirname "$tarball")/masir-desktop-linux-$version-$arch.rpm
cp "$rpm_out" "$dest"
echo "==> $dest"
