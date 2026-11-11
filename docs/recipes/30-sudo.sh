#!/bin/sh
# sudo を導入し、ユーザーに sudo の権限を与える。冪等。
#
# 置き場所: post.d/  (ユーザーが、pre.d などで作られたあと)
#
# 環境変数:
#   SUDO_USER_NAME  対象のユーザー。省略時は DCB_USER
#   SUDO_NOPASSWD   1 ならパスワード無し (NOPASSWD)。省略時は、パスワードが要る
#
# 注意: NOPASSWD は、コンテナの中の、そのユーザーが、root になれる設定。
# 事前ビルドして、公開するイメージでは、必要かどうかを、よく考える。
#
# /etc/sudoers.d/<ユーザー> を 0440 で書き、visudo があれば、構文を検証する。
# sudo は、名前に "." を含むファイルや、"~" で終わるファイルを、黙って無視するので、
# ユーザー名の文字を、英数字と "_" と "-" に限る。
set -eu
. "$(dirname "$0")/_lib.sh"

user="${SUDO_USER_NAME:-${DCB_USER:-}}"
[ -n "$user" ] || die "SUDO_USER_NAME or DCB_USER is required"
[ "$user" != root ] || die "refusing to write a sudoers entry for root"
case "$user" in
  *[!A-Za-z0-9_-]* | -*) die "unsupported user name for sudoers: $user" ;;
esac
id "$user" >/dev/null 2>&1 || die "user does not exist: $user (create it in an earlier script)"

installed=0
if ! have sudo; then
  pkg_install sudo
  installed=1
fi

if [ "${SUDO_NOPASSWD:-}" = 1 ]; then
  line="$user ALL=(ALL) NOPASSWD:ALL"
  log "WARNING: $user can become root without a password"
else
  line="$user ALL=(ALL) ALL"
fi

mkdir -p /etc/sudoers.d
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
printf '%s\n' "$line" >"$tmp"
chmod 0440 "$tmp"
if have visudo; then
  visudo -cf "$tmp" >/dev/null || die "invalid sudoers entry: $line"
fi

# /etc/sudoers が、/etc/sudoers.d を読み込むようにする ("#includedir" は、コメントではなく指令)。
if [ -f /etc/sudoers ] && ! grep -Eq '^[#@]includedir[[:space:]]+/etc/sudoers\.d' /etc/sudoers; then
  printf '#includedir /etc/sudoers.d\n' >>/etc/sudoers
fi

cat "$tmp" >"/etc/sudoers.d/$user"
chmod 0440 "/etc/sudoers.d/$user"
chown root:root "/etc/sudoers.d/$user"
if have visudo; then
  visudo -c >/dev/null || die "sudoers is invalid after the change"
fi

[ "$installed" -eq 0 ] || pkg_clean
log "sudo enabled for $user"
