#!/bin/sh
# Downloads Wintun, which Xray's TUN needs on Windows, into build/wintun/<arch>, for the
# Windows release archives. The archive is pinned by its SHA-256 as published on
# wintun.net. Its license allows shipping wintun.dll alongside software that uses it
# only through the Wintun API, as Xray does; the license ships with it.
set -eu

version=0.14.1
sha256=07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51
out=build/wintun
mkdir -p "$out"

zip="$out/wintun-$version.zip"
curl -fsSL --retry 3 -o "$zip" "https://www.wintun.net/builds/wintun-$version.zip"
if command -v sha256sum >/dev/null; then
	got=$(sha256sum "$zip" | awk '{print $1}')
else
	got=$(shasum -a 256 "$zip" | awk '{print $1}')
fi
if [ "$got" != "$sha256" ]; then
	echo "wintun-$version.zip: checksum mismatch" >&2
	exit 1
fi

rm -rf "$out/x"
unzip -q "$zip" -d "$out/x"
for arch in amd64 arm64; do
	mkdir -p "$out/$arch"
	cp "$out/x/wintun/bin/$arch/wintun.dll" "$out/$arch/wintun.dll"
	cp "$out/x/wintun/LICENSE.txt" "$out/$arch/wintun-LICENSE.txt"
done
rm -rf "$out/x" "$zip"
echo "wintun $version ok"
