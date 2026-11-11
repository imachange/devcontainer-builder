#!/bin/sh
# ユーザーを作る。すでにあれば、何もしない (冪等)。
#
# 置き場所: pre.d/  (files/home が、ユーザーとそのホームを必要とするため、ファイル配置より前に作る)
#
# 環境変数 (build.args に書くと、スクリプトの環境変数になる):
#   USER_NAME   作るユーザー名。省略時は DCB_USER (remoteUser、containerUser、BUILD_USER の順で決まる)
#   USER_UID    UID。省略時は、システムが決める (Ubuntu 24.04 は 1000 が、"ubuntu" で使われている)
#   USER_GID    GID。省略時は、システムが決める
#   USER_SHELL  シェル。省略時は /bin/bash (無ければ /bin/sh)
#
# ディストリビューションごとの違い:
#   Debian / Ubuntu / Fedora / openSUSE   useradd -m
#   Alpine (busybox)                      adduser -D
#   UBI minimal                           useradd が無いので、shadow-utils を導入する
set -eu
. "$(dirname "$0")/_lib.sh"

name="${USER_NAME:-${DCB_USER:-}}"
[ -n "$name" ] || die "USER_NAME or DCB_USER is required"
case "$name" in
  *[!A-Za-z0-9_-]* | -*) die "unsupported user name: $name" ;;
esac
if [ "$name" = root ]; then
  log "user root already exists"
  exit 0
fi
if id "$name" >/dev/null 2>&1; then
  log "user $name already exists"
  exit 0
fi

shell="${USER_SHELL:-}"
if [ -z "$shell" ]; then
  if [ -x /bin/bash ]; then shell=/bin/bash; else shell=/bin/sh; fi
fi

case "${DCB_OS_FAMILY:-}" in
  apk)
    if [ -n "${USER_GID:-}" ]; then addgroup -g "$USER_GID" "$name"; else addgroup "$name"; fi
    set -- -D -s "$shell" -G "$name"
    [ -z "${USER_UID:-}" ] || set -- "$@" -u "$USER_UID"
    adduser "$@" "$name"
    ;;
  *)
    if ! have useradd; then
      case "${DCB_OS_FAMILY:-}" in
        dnf) pkg_install shadow-utils ;;
        zypper) pkg_install shadow ;;
        apt) pkg_install passwd ;;
        *) die "useradd is not available and the distribution is unknown" ;;
      esac
      pkg_clean
    fi
    set -- -m -s "$shell"
    if [ -n "${USER_GID:-}" ]; then
      getent group "$name" >/dev/null 2>&1 || groupadd -g "$USER_GID" "$name"
      set -- "$@" -g "$name"
    fi
    [ -z "${USER_UID:-}" ] || set -- "$@" -u "$USER_UID"
    useradd "$@" "$name"
    ;;
esac

log "created user $name"
