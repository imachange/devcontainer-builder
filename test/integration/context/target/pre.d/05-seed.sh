#!/bin/sh
# マージ先になる、既存の設定ファイルを用意する (構造化ファイルへの追記のテスト用)。
set -eu
mkdir -p /etc/dcb-test
printf '{"a": 1}\n' > /etc/dcb-test/settings.json
