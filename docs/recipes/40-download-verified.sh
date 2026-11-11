#!/bin/sh
# チェックサムを検証して、ファイルをダウンロードする。冪等。
#
# 置き場所: post.d/  (curl と ca-certificates は、packages で導入しておくと、確実)
#
# 環境変数 (build.args に書くと、スクリプトの環境変数になる):
#   DOWNLOAD_URL      https:// の URL (必須)
#   DOWNLOAD_SHA256   期待する SHA-256 (必須。検証に失敗したら、何も配置しない)
#   DOWNLOAD_DEST     配置先 (必須)。DOWNLOAD_EXTRACT を指定したときは、展開先のディレクトリ
#   DOWNLOAD_MODE     配置するファイルの権限。既定 0644
#   DOWNLOAD_EXTRACT  tar を展開するなら 1 (tar.gz / tar.xz / tar.bz2 は、tar が判別する)
#   DOWNLOAD_STRIP    展開時に落とす、先頭のディレクトリの数。既定 0
#
# チェックサムが無い取得は、再現性も安全性も保証できないので、省略できない。
set -eu
. "$(dirname "$0")/_lib.sh"

url="${DOWNLOAD_URL:-}"
sha="${DOWNLOAD_SHA256:-}"
dest="${DOWNLOAD_DEST:-}"
[ -n "$url" ] && [ -n "$sha" ] && [ -n "$dest" ] || die "DOWNLOAD_URL, DOWNLOAD_SHA256 and DOWNLOAD_DEST are required"

case "$url" in
  https://*) ;;
  file://*) [ "${DOWNLOAD_ALLOW_FILE:-}" = 1 ] || die "file:// URLs need DOWNLOAD_ALLOW_FILE=1 (for tests only)" ;;
  *) die "only https:// URLs are allowed: $url" ;;
esac
case "$sha" in
  *[!0-9A-Fa-f]*) die "DOWNLOAD_SHA256 must be hexadecimal" ;;
esac
[ "${#sha}" -eq 64 ] || die "DOWNLOAD_SHA256 must be 64 hexadecimal characters"
sha="$(printf '%s' "$sha" | tr 'A-F' 'a-f')"

sha_of() { sha256sum "$1" | cut -d' ' -f1; }

# 展開しない場合に限り、配置済みで、同じ内容なら、何もしない。
if [ "${DOWNLOAD_EXTRACT:-}" != 1 ] && [ -f "$dest" ] && [ "$(sha_of "$dest")" = "$sha" ]; then
  log "already present: $dest"
  exit 0
fi

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

case "$url" in
  file://*)
    # テスト用の file:// スキームは wget / curl を使わず直接コピー
    cp "${url#file://}" "$tmp"
    ;;
  *)
    if ! have curl && ! have wget; then
      case "${DCB_OS_FAMILY:-}" in
        apt | dnf | zypper | apk) pkg_install curl ca-certificates ;;
        *) die "neither curl nor wget is available" ;;
      esac
      pkg_clean
    fi

    if have curl; then
      curl -fsSL --retry 3 --proto '=https' -o "$tmp" "$url"
    else
      wget -q -O "$tmp" "$url"
    fi
    ;;
esac

actual="$(sha_of "$tmp")"
[ "$actual" = "$sha" ] || die "checksum mismatch for $url: expected $sha, got $actual"

if [ "${DOWNLOAD_EXTRACT:-}" = 1 ]; then
  mkdir -p "$dest"
  tar -xf "$tmp" -C "$dest" --strip-components="${DOWNLOAD_STRIP:-0}"
else
  mkdir -p "$(dirname "$dest")"
  cp "$tmp" "$dest.tmp"
  chmod "${DOWNLOAD_MODE:-0644}" "$dest.tmp"
  mv "$dest.tmp" "$dest"
fi
log "downloaded and verified: $url"