#!/bin/sh
# post フェーズの環境を、記録する (DCB_* とテンプレート変数が、渡されていること)。
set -eu
{
  echo "phase=$DCB_PHASE"
  echo "family=$DCB_OS_FAMILY"
  echo "pkgmgr=$DCB_PKGMGR"
  echo "user=$DCB_USER"
  echo "app_env=$APP_ENV"
} > /etc/dcb-test/post-ran
