# shellcheck shell=sh
#
# レシピ共通の部品。各レシピは、次のように読み込む。
#
#     . "$(dirname "$0")/_lib.sh"
#
# "_" で始まるファイルは、devcontainer-builder が実行しない (部品として、同じディレクトリから読み込める)。
# devcontainer-builder が渡す環境変数 (DCB_PKGMGR、DCB_OS_FAMILY など) を使う。

log() { printf '==> %s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# pkg_install PKG...
#   検出されたパッケージマネージャ (DCB_PKGMGR) で、パッケージを導入する。
#   パッケージ名は、ディストリビューションごとに違うので、呼び出し側が、系統 (DCB_OS_FAMILY) で選ぶ。
pkg_install() {
  case "${DCB_PKGMGR:-}" in
    apt-get)
      apt-get update -qq
      DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "$@"
      ;;
    dnf) dnf install -y "$@" ;;
    microdnf) microdnf install -y "$@" ;;
    yum) yum install -y "$@" ;;
    apk) apk add --no-cache "$@" ;;
    zypper) zypper --non-interactive install --no-recommends "$@" ;;
    *) die "no package manager found (DCB_PKGMGR is empty); install it in the base image or an earlier pre.d script" ;;
  esac
}

# pkg_clean
#   pkg_install が残した、キャッシュを消す。イメージを小さく保つため、導入したレシピの最後に呼ぶ。
pkg_clean() {
  case "${DCB_PKGMGR:-}" in
    apt-get) rm -rf /var/lib/apt/lists/* ;;
    dnf) dnf clean all ;;
    microdnf) microdnf clean all ;;
    yum) yum clean all ;;
    zypper) zypper --non-interactive clean --all ;;
    *) : ;;
  esac
}

download_file() {
    local url="$1"
    local dest="$2"

    case "$url" in
        file://*)
            # file:// スキームの場合はパスを取り出して cp で直接コピー
            cp "${url#file://}" "$dest"
            ;;
        *)
            if command -v curl >/dev/null 2>&1; then
                curl -fsSL "$url" -o "$dest"
            else
                wget -O "$dest" "$url"
            fi
            ;;
    esac
}