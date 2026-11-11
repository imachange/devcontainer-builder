#!/bin/sh
# タイムゾーンと、ロケール (既定は、日本語) を設定する。冪等。
#
# 置き場所: post.d/
#
# 環境変数:
#   TZ_NAME       タイムゾーン。既定 Asia/Tokyo
#   LOCALES       生成するロケール (空白区切り)。既定 "ja_JP.UTF-8 en_US.UTF-8"
#   DEFAULT_LANG  既定の LANG。既定は LOCALES の先頭
#
# 重要: ロケールの生成と、/etc/default/locale (または /etc/locale.conf) の設定までを行う。
# docker exec や VS Code のターミナルに効くのは、環境変数 LANG だけで、これは、RUN からは設定できない。
# LANG は、ベースイメージの ENV か、利用側の devcontainer.json の containerEnv で決める。
#
# ディストリビューションごとの違い:
#   apt     locales を導入し、locale-gen を実行する
#   dnf     glibc-langpack-<言語> を導入する (未提供の言語パックはスキップ)
#   zypper  glibc-locale を導入する
#   apk     musl には、glibc のようなロケールの仕組みが無いので、何もしない (タイムゾーンだけ設定する)
set -eu
. "$(dirname "$0")/_lib.sh"

tz="${TZ_NAME:-Asia/Tokyo}"
locales="${LOCALES:-ja_JP.UTF-8 en_US.UTF-8}"
default_lang="${DEFAULT_LANG:-${locales%% *}}"
installed=0

case "$tz" in
  *[!A-Za-z0-9_+/-]* | /* | *..*) die "unsupported time zone: $tz" ;;
esac

# ---- タイムゾーン
if [ ! -e "/usr/share/zoneinfo/$tz" ]; then
  case "${DCB_OS_FAMILY:-}" in
    zypper) pkg_install timezone ;;
    *) pkg_install tzdata ;;
  esac
  installed=1
fi
[ -e "/usr/share/zoneinfo/$tz" ] || die "unknown time zone: $tz"
ln -sf "/usr/share/zoneinfo/$tz" /etc/localtime
echo "$tz" >/etc/timezone
log "time zone: $tz"

# ---- ロケール
norm() { printf '%s' "$1" | sed -e 's/UTF-8/utf8/' -e 's/utf-8/utf8/'; }

case "${DCB_OS_FAMILY:-}" in
  apt)
    if ! have locale-gen; then
      pkg_install locales
      installed=1
    fi
    touch /etc/locale.gen
    for l in $locales; do
      if ! grep -q "^$l UTF-8" /etc/locale.gen; then
        sed -i "s/^# *\($l UTF-8\)/\1/" /etc/locale.gen
        grep -q "^$l UTF-8" /etc/locale.gen || echo "$l UTF-8" >>/etc/locale.gen
      fi
    done
    locale-gen
    printf 'LANG=%s\n' "$default_lang" >/etc/default/locale
    ;;
  dnf)
    for l in $locales; do
      lang="${l%%_*}"
      # UBI 9 Minimal 等で一部の言語パック (glibc-langpack-ja など) がリポジトリに存在しない場合はスキップ
      if pkg_install "glibc-langpack-$lang" 2>/dev/null; then
        installed=1
      else
        log "glibc-langpack-$lang is not available in repositories; skipping"
      fi
    done
    printf 'LANG=%s\n' "$default_lang" >/etc/locale.conf
    ;;
  zypper)
    pkg_install glibc-locale
    installed=1
    printf 'LANG=%s\n' "$default_lang" >/etc/locale.conf
    ;;
  apk)
    log "musl has no locale database; skipping locales (set LANG=C.UTF-8 in the base image if you need UTF-8)"
    ;;
  *)
    log "unknown distribution family; skipping locales"
    ;;
esac

# ---- 確認 (glibc 系だけ)
if [ "${DCB_OS_FAMILY:-}" != apk ] && have locale; then
  available="$(locale -a 2>/dev/null || true)"
  for l in $locales; do
    if ! printf '%s\n' "$available" | grep -qix "$(norm "$l")"; then
      log "warning: locale was not generated: $l (may not be supported in this base image)"
    fi
  done
  log "locales: $locales (default $default_lang)"
fi

[ "$installed" -eq 0 ] || pkg_clean