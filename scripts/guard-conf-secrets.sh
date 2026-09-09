#!/usr/bin/env bash
# 硬闸：阻止「还没抄走密钥就 git pull」这条不可逆的数据损失路径。（台账 #73 衍生）
#
# 背景（2026-09-09 只读实测）：
#   服务器 conf/conf_pro.yaml 里 security.jwt_secret / security.config_encryption_key
#   仍有值，而仓库 main 已把这两行清空。部署仪式里的 git reset --hard / git pull 会
#   快进覆盖该文件 —— 服务器上唯一一份 config_encryption_key 当场消失。
#   它是 AES-256 key（backend/internal/secure/secrets.go:15-33），加密着
#   quanty_trade.users.configs（实测 2 行用户）。换一把新的 = 那些配置永久解不开。
#   线上容器的 Config.Env 里没有这两个键，所以别处没有第二份。
#
# 本脚本只判断「键在不在、值空不空」。
# 绝不读取、比较、打印、落盘任何值 —— 全程只用 grep -q 的退出码，不产生输出。
#
# 用法： guard_conf_secrets   # 返回 0=可以 pull；非 0=会丢密钥，已打印怎么办
# 调用方：pre.sh（pull 之前）、server_deploy_backend.sh --check

# yaml 里某个键是否有非空值。`key: ""` 和 `key:` 都算空；`key: v  # 注释` 算非空。
_yaml_key_live() {
  grep -qE "^[[:space:]]*$2:[[:space:]]*[\"']?[^\"'[:space:]#]" "$1"
}

# env 文件里某个键是否有非空值。`KEY=` 和 `KEY=""` 都算空。
_env_key_live() {
  grep -qE "^[[:space:]]*(export[[:space:]]+)?$2=[\"']?[^\"'[:space:]#]" "$1"
}

guard_conf_secrets() {
  local repo_root="${1:-.}"
  local env_file="${2:-/etc/quanty/backend.env}"
  local conf="$repo_root/conf/conf_pro.yaml"

  # 文件不存在 = 没有活体副本 = pull 抹不掉任何东西，放行。
  # 这里故意不 fail closed：构建机、新 clone 都没有这个文件，在那儿拦一下纯属误报，
  # 而误报会让人养成 `|| true` 绕过的习惯，真出事那次也就一起绕过去了。
  [ -f "$conf" ] || return 0

  # 存在但读不了（多半是没用 root 跑），无法判断就别乱放行也别乱拦，说清楚让人换身份重跑。
  if [ ! -r "$conf" ]; then
    echo "错误: 读不了 $conf，无法判断 pull 会不会抹掉密钥。请用能读它的身份重跑（服务器上是 root）。" >&2
    return 1
  fi
  if [ -e "$env_file" ] && [ ! -r "$env_file" ]; then
    echo "错误: $env_file 存在但读不了，无法判断密钥是否已抄走。请用 root 重跑。" >&2
    return 1
  fi

  # 两两配对：yaml 里还活着、而 env 里还没有 → 这一把就是「只此一份」。
  local at_risk=""
  local pair
  for pair in "jwt_secret:JWT_SECRET" "config_encryption_key:CONFIG_ENCRYPTION_KEY"; do
    local ykey="${pair%%:*}" ekey="${pair##*:}"
    # 写成 if 而不是 `A && B && C`：后者整体为假时会在调用方的 set -e 下把脚本带走。
    if _yaml_key_live "$conf" "$ykey"; then
      if [ -f "$env_file" ] && _env_key_live "$env_file" "$ekey"; then
        : # 已经抄进 env 了，pull 抹掉 yaml 里那份也无所谓
      else
        at_risk="$at_risk $ekey"
      fi
    fi
  done

  [ -n "$at_risk" ] || return 0

  echo "" >&2
  echo "拒绝继续：下列密钥目前只有服务器 conf/conf_pro.yaml 一份，$env_file 里还没有：" >&2
  echo "   $at_risk" >&2
  echo "" >&2
  echo "git pull / git reset --hard 会把 conf_pro.yaml 快进成仓库里那份已清空的版本，" >&2
  echo "这两个值会当场消失，而且别处没有第二份（线上容器的环境变量里也没有）。" >&2
  case "$at_risk" in
    *CONFIG_ENCRYPTION_KEY*)
      echo "其中 CONFIG_ENCRYPTION_KEY 丢了不可逆：users.configs（实测 2 行用户）是用它加密的，" >&2
      echo "换一把新的 = 这些用户的交易所配置永久解不开，没有任何补救手段。" >&2
      ;;
  esac
  echo "" >&2
  echo "怎么办（只有所有者能做，约 5 分钟）：照 docs/deploy-preflight.md 第 3.1.1 节" >&2
  echo "『先抄密钥再 pull』把值从 conf_pro.yaml 抄进 $env_file，然后重跑本命令。" >&2
  echo "抄完自检： bash server_deploy_backend.sh --check" >&2
  echo "" >&2
  return 1
}

# 允许直接执行：bash scripts/guard-conf-secrets.sh [repo_root] [env_file]
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  guard_conf_secrets "${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}" \
                     "${2:-${QUANTY_ENV_FILE:-/etc/quanty/backend.env}}"
fi
