#!/bin/bash

# 下面的 reset --hard / pull 会快进覆盖 conf/conf_pro.yaml。服务器上那份是唯一
# 还带着 jwt_secret / config_encryption_key 的副本，覆盖 = 永久丢 key（详见
# scripts/guard-conf-secrets.sh 顶部与 docs/deploy-preflight.md 第 3.1 节）。
# 所以先过闸，闸不过就一步都不走。
PRE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ ! -r "$PRE_DIR/scripts/guard-conf-secrets.sh" ]; then
  echo "错误: 找不到 $PRE_DIR/scripts/guard-conf-secrets.sh，无法判断 pull 会不会抹掉密钥。" >&2
  echo "      本脚本默认动作就是不可逆覆盖，验不了就不做。" >&2
  exit 1
fi
# shellcheck source=scripts/guard-conf-secrets.sh
. "$PRE_DIR/scripts/guard-conf-secrets.sh"
guard_conf_secrets "$PRE_DIR" "${QUANTY_ENV_FILE:-/etc/quanty/backend.env}" || exit 1

git fetch --all

git reset --hard origin/main

git pull origin main

chmod +x server_deploy_*

chmod +x pre.sh