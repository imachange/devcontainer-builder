#!/bin/sh
# 統合テストの検証。apply を 2 回重ねたイメージ (stage2) の中で、実行する。
#
#   docker run --rm \
#     -v "$PWD/test/integration/verify.sh:/verify.sh:ro" \
#     -v "$PWD/docs/recipes:/recipes:ro" \
#     it:stage2 sh /verify.sh
#
# busybox (Alpine) でも動くように、POSIX sh と、busybox にもあるオプションだけを使う。
set -u

user=dcbuser
fail=0

ok() { printf 'ok   - %s\n' "$1"; }
bad() {
  printf 'FAIL - %s\n' "$1"
  fail=1
}
note() { printf 'note - %s\n' "$1"; }

# t "説明" '条件 (シェルの式)'
t() {
  desc=$1
  shift
  if eval "$@"; then ok "$desc"; else bad "$desc"; fi
}

mode_of() { stat -c '%a' "$1"; }
owner_of() { stat -c '%U' "$1"; }
post=/etc/dcb-test/post-ran
family="$(sed -n 's/^family=//p' "$post" 2>/dev/null)"
pkgmgr="$(sed -n 's/^pkgmgr=//p' "$post" 2>/dev/null)"

echo "== family=$family pkgmgr=$pkgmgr"

# ---- pre.d: ユーザー作成 (レシピ 10)
t "user exists" 'id "$user" >/dev/null 2>&1'
t "home exists and is owned by the user" '[ "$(owner_of /home/$user)" = "$user" ]'

# ---- packages
t "git is installed (packages/default.txt)" 'command -v git >/dev/null 2>&1'
t "curl is installed (BUILD_PACKAGES)" 'command -v curl >/dev/null 2>&1'

# ---- files/root
t "template is expanded" 'grep -qx "env=ci" /etc/dcb-test/app.conf'
t "JSON is merged (existing key kept)" 'grep -q "\"a\"" /etc/dcb-test/settings.json'
t "JSON is merged (new key added)" 'grep -q "\"b\"" /etc/dcb-test/settings.json'
t "text append is idempotent across layers" '[ "$(grep -c dcb-test-host /etc/dcb-test/hosts.txt)" = 1 ]'

# ---- files/home
t ".ssh is 0700 and owned by the user" '[ "$(mode_of /home/$user/.ssh)" = 700 ] && [ "$(owner_of /home/$user/.ssh)" = "$user" ]'
t ".ssh/config is 0600 and owned by the user" '[ "$(mode_of /home/$user/.ssh/config)" = 600 ] && [ "$(owner_of /home/$user/.ssh/config)" = "$user" ]'
t ".bashrc has the appended line exactly once" '[ "$(grep -c "alias dcb=" /home/$user/.bashrc)" = 1 ]'
t ".bashrc is owned by the user" '[ "$(owner_of /home/$user/.bashrc)" = "$user" ]'

# ---- post.d
t "post script saw DCB_PHASE=post" 'grep -qx "phase=post" "$post"'
t "post script saw the detected family" '[ -n "$family" ]'
t "post script saw the detected package manager" '[ -n "$pkgmgr" ]'
t "post script saw DCB_USER" 'grep -qx "user=$user" "$post"'
t "post script saw build.args" 'grep -qx "app_env=ci" "$post"'

# ---- レシピ 20: タイムゾーンとロケール
t "time zone is Asia/Tokyo" 'readlink /etc/localtime | grep -q Asia/Tokyo'
t "/etc/timezone is Asia/Tokyo" '[ "$(cat /etc/timezone)" = Asia/Tokyo ]'
if [ "$family" = apk ]; then
  note "musl: locales are not generated"
else
  t "Japanese locale is generated" 'locale -a | grep -Eqi "^ja_JP\.utf-?8$"'
fi

# ---- レシピ 30: sudo
t "sudoers entry is 0440 root-owned" '[ "$(mode_of /etc/sudoers.d/$user)" = 440 ] && [ "$(owner_of /etc/sudoers.d/$user)" = root ]'
t "sudoers entry grants the user" 'grep -q "^$user ALL=(ALL) NOPASSWD:ALL" /etc/sudoers.d/$user'

# ---- レシピ 40: チェックサム付きダウンロード
t "download was verified and placed" '[ "$(cat /opt/dcb/payload.txt)" = "dcb payload" ]'

# ---- 後片付け
t "no temporary script directories are left" '! ls /tmp /var/tmp 2>/dev/null | grep -q "^dcb-"'
t "no temporary files are left next to placed files" '! find /etc /home /opt -name ".dcb-*" 2>/dev/null | grep -q .'
if [ "$family" = apt ]; then
  t "apt package lists are removed" '[ -z "$(ls /var/lib/apt/lists 2>/dev/null | grep -v -e "^lock$" -e "^partial$")" ]'
fi

# 情報 (失敗にしない): RUN --mount のマウント先が、イメージに残っているか。
for p in /tmp/apply /tmp/ctx; do
  if [ -e "$p" ]; then note "mount point $p is left in the image"; else note "mount point $p is not in the image"; fi
done

# ---- レシピの単体の動作 (イメージの中で実行する)
if [ -d /recipes ]; then
  export DCB_OS_FAMILY="$family" DCB_PKGMGR="$pkgmgr"

  t "recipe 10 is idempotent" 'DCB_USER=$user sh /recipes/10-create-user.sh >/dev/null 2>&1'
  t "recipe 10 rejects a bad user name" '! DCB_USER="bad name" sh /recipes/10-create-user.sh >/dev/null 2>&1'
  t "recipe 30 refuses root" '! DCB_USER=root sh /recipes/30-sudo.sh >/dev/null 2>&1'

  zeros="0000000000000000000000000000000000000000000000000000000000000000"
  rm -f /tmp/should-not-exist
  t "recipe 40 fails on a wrong checksum and places nothing" \
    '! DOWNLOAD_URL=file:///etc/dcb-test/payload.txt DOWNLOAD_SHA256=$zeros DOWNLOAD_DEST=/tmp/should-not-exist DOWNLOAD_ALLOW_FILE=1 sh /recipes/40-download-verified.sh >/dev/null 2>&1 && [ ! -e /tmp/should-not-exist ]'
  t "recipe 40 rejects plain http" '! DOWNLOAD_URL=http://example.invalid/x DOWNLOAD_SHA256=$zeros DOWNLOAD_DEST=/tmp/x sh /recipes/40-download-verified.sh >/dev/null 2>&1'
else
  note "/recipes is not mounted; skipping the recipe checks"
fi

echo
if [ "$fail" -eq 0 ]; then
  echo "all checks passed"
else
  echo "some checks failed"
fi
exit "$fail"
