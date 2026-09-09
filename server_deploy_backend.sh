#!/usr/bin/env bash
set -euo pipefail

# 凭据一律不写进本脚本（本仓库是公开的）。真实值放在服务器本地的 600 权限
# env 文件里，默认 /etc/quanty/backend.env，可用 QUANTY_ENV_FILE 覆盖路径。
# 该文件格式为每行 KEY=VALUE，示例见本脚本末尾注释。
QUANTY_ENV_FILE="${QUANTY_ENV_FILE:-/etc/quanty/backend.env}"
if [ -f "$QUANTY_ENV_FILE" ]; then
  perm="$(stat -c '%a' "$QUANTY_ENV_FILE" 2>/dev/null || stat -f '%Lp' "$QUANTY_ENV_FILE" 2>/dev/null || echo '')"
  if [ -n "$perm" ] && [ "$perm" != "600" ]; then
    echo "错误: $QUANTY_ENV_FILE 权限是 $perm，必须是 600。执行: chmod 600 $QUANTY_ENV_FILE" >&2
    exit 1
  fi
  set -a
  # shellcheck disable=SC1090
  . "$QUANTY_ENV_FILE"
  set +a
fi

DOCKER_HUB_ID="zhaoxianxinclimber108"
BACKEND_IMAGE="${DOCKER_HUB_ID}/quanty_trade-backend"
BACKEND_VERSION="${BACKEND_VERSION:-}"

CONTAINER_NAME="quanty-backend"
HOST_PORT="8080"

DB_TYPE="mysql"
DB_HOST="137.220.219.172"
DB_PORT="3306"
DB_USER="quanty"
DB_PASS="${DB_PASS:-}"
DB_NAME="quanty_trade"

STRATEGIES_DIR="/root/quanty_trade/strategies"

REDIS_ENABLED="true"
REDIS_ADDR="137.220.219.172" # e.g. 127.0.0.1:6379 or <redis-ip>:6379
REDIS_PASSWORD="${REDIS_PASSWORD:-}"
REDIS_DB="0"
REDIS_PREFIX="qt"

EXCHANGE="binance"
BINANCE_MARKET="usdm"
BINANCE_API_KEY="${BINANCE_API_KEY:-}"
BINANCE_API_SECRET="${BINANCE_API_SECRET:-}"
TELEGRAM_ENABLED="true"
TELEGRAM_BOT_TOKEN="${TELEGRAM_BOT_TOKEN:-}"
TELEGRAM_POLL_TIMEOUT_SECONDS="30"

# conf/conf_pro.yaml 已清空所有密钥字段，这几个必须由 env 提供，否则
# 后端启动期 conf.MustValidateSecurity() 会 panic 拒启。
JWT_SECRET="${JWT_SECRET:-}"
CONFIG_ENCRYPTION_KEY="${CONFIG_ENCRYPTION_KEY:-}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-}"
OPENROUTER_API_KEY="${OPENROUTER_API_KEY:-}"
LARK_WEBHOOK_URL="${LARK_WEBHOOK_URL:-}"
LARK_SECRET="${LARK_SECRET:-}"

# 必填校验：缺任何一个都直接退出，不使用默认值、不静默降级。
missing=""
for name in DB_PASS REDIS_PASSWORD BINANCE_API_KEY BINANCE_API_SECRET \
            JWT_SECRET CONFIG_ENCRYPTION_KEY; do
  eval "val=\${$name}"
  [ -z "$val" ] && missing="$missing $name"
done
if [ -n "$missing" ]; then
  echo "错误: 以下必填凭据未设置:$missing" >&2
  echo "请写入 $QUANTY_ENV_FILE (chmod 600)，或先 export 再运行本脚本。" >&2
  exit 1
fi

docker version >/dev/null

if [ "$BACKEND_VERSION" = "REPLACE_BACKEND_TAG" ] || [ -z "$BACKEND_VERSION" ]; then
  echo "请先在脚本顶部填写 BACKEND_VERSION（后端镜像 tag）"
  exit 1
fi

if [ "$DB_TYPE" = "mysql" ]; then
  if [ "$DB_HOST" = "REPLACE_DB_HOST" ] || [ "$DB_USER" = "REPLACE_DB_USER" ]; then
    echo "请先在脚本顶部填写数据库配置：DB_HOST DB_USER（DB_PASS 走 env 文件）"
    exit 1
  fi
fi

if [ "${REDIS_ENABLED}" = "true" ]; then
  if [ "$REDIS_ADDR" = "REPLACE_REDIS_ADDR" ] || [ -z "$REDIS_ADDR" ]; then
    echo "请先在脚本顶部填写 REDIS_ADDR"
    exit 1
  fi
  if [ "$REDIS_PASSWORD" = "REPLACE_REDIS_PASSWORD" ]; then
    echo "请先在脚本顶部填写 REDIS_PASSWORD（如无密码可置空）"
    exit 1
  fi

  if [[ "$REDIS_ADDR" != *:* ]]; then
    REDIS_ADDR="${REDIS_ADDR}:6379"
  fi
fi

docker pull "${BACKEND_IMAGE}:${BACKEND_VERSION}"

docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true

mkdir -p "${STRATEGIES_DIR}"

find "${STRATEGIES_DIR}" -mindepth 1 -maxdepth 1 -exec rm -rf {} +

SEED_CONTAINER="${CONTAINER_NAME}-seed-$$"
docker create --name "${SEED_CONTAINER}" "${BACKEND_IMAGE}:${BACKEND_VERSION}" >/dev/null
docker cp "${SEED_CONTAINER}:/app/strategies/." "${STRATEGIES_DIR}/"
docker rm -f "${SEED_CONTAINER}" >/dev/null 2>&1 || true

# 做市/交易所密钥从服务器本地 .env 注入(文件存在才挂;缺省不影响 observe)。.env 已被 .gitignore 忽略,勿提交、勿外发。
MM_ENV_FILE="/root/work/quanty_trade/.env"
MM_ENV_ARG=""
[ -f "$MM_ENV_FILE" ] && MM_ENV_ARG="--env-file $MM_ENV_FILE"

docker run -d \
  ${MM_ENV_ARG} \
  --name "${CONTAINER_NAME}" \
  --restart always \
  --log-opt max-size=50m \
  --log-opt max-file=3 \
  -p "${HOST_PORT}:8080" \
  -v "${STRATEGIES_DIR}:/app/strategies" \
  -v "/root/work/quanty_trade/conf:/app/conf" \
  -e PORT=8080 \
  -e SERVER_PORT=8080 \
  -e GIN_MODE=release \
  -e DB_TYPE="${DB_TYPE}" \
  -e DB_USER="${DB_USER}" \
  -e DB_PASS="${DB_PASS}" \
  -e DB_HOST="${DB_HOST}" \
  -e DB_PORT="${DB_PORT}" \
  -e DB_NAME="${DB_NAME}" \
  -e STRATEGIES_DIR="/app/strategies" \
  -e REDIS_ENABLED="${REDIS_ENABLED}" \
  -e REDIS_ADDR="${REDIS_ADDR}" \
  -e REDIS_PASSWORD="${REDIS_PASSWORD}" \
  -e REDIS_DB="${REDIS_DB}" \
  -e REDIS_PREFIX="${REDIS_PREFIX}" \
  -e EXCHANGE="${EXCHANGE}" \
  -e BINANCE_MARKET="${BINANCE_MARKET}" \
  -e EXCHANGE="binance" \
  -e BINANCE_MARKET="usdm" \
  -e BINANCE_API_KEY="${BINANCE_API_KEY}" \
  -e BINANCE_API_SECRET="${BINANCE_API_SECRET}" \
  -e TELEGRAM_ENABLED="${TELEGRAM_ENABLED}" \
  -e TELEGRAM_BOT_TOKEN="${TELEGRAM_BOT_TOKEN}" \
  -e TELEGRAM_POLL_TIMEOUT_SECONDS="${TELEGRAM_POLL_TIMEOUT_SECONDS}" \
  -e JWT_SECRET="${JWT_SECRET}" \
  -e CONFIG_ENCRYPTION_KEY="${CONFIG_ENCRYPTION_KEY}" \
  -e ADMIN_PASSWORD="${ADMIN_PASSWORD}" \
  -e OPENROUTER_API_KEY="${OPENROUTER_API_KEY}" \
  -e LARK_WEBHOOK_URL="${LARK_WEBHOOK_URL}" \
  -e LARK_SECRET="${LARK_SECRET}" \
  -e MARKETMAKER_CONFIG=/app/conf/marketmaker.json \
  -e REBALANCE_CONFIG=/app/conf/rebalance.yaml \
  "${BACKEND_IMAGE}:${BACKEND_VERSION}" >/dev/null

echo "后端部署完成"
echo "Backend: http://<server-ip>:${HOST_PORT}"

# ---------------------------------------------------------------------------
# 记录"线上真正在跑的是什么"（台账 #59）。
# 全部字段从 docker inspect 实读，不用 ${BACKEND_VERSION} 这个"意图"变量——
# 意图和现实不一致正是 #59 的根因：以前只有 deploy.sh（构建机）写这个文件，
# 真正起容器的本脚本一个字都不写，于是服务器上的记录停在最后一次"在服务器上
# 跑过完整部署"的那天，实际镜像却早换了好几轮。事故时按它回滚 = 回错版本。
RECORD_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
running_tag_full="$(docker inspect "${CONTAINER_NAME}" --format '{{.Config.Image}}')"
running_tag="${running_tag_full##*:}"
running_image_id="$(docker inspect "${CONTAINER_NAME}" --format '{{.Image}}')"
image_created="$(docker image inspect "${running_image_id}" --format '{{.Created}}')"
container_id="$(docker inspect "${CONTAINER_NAME}" --format '{{.Id}}')"
container_started="$(docker inspect "${CONTAINER_NAME}" --format '{{.State.StartedAt}}')"

# 构建时的 commit 编码在 tag 中段（deploy.sh:generate_version 产出
# "<时间戳>-<short sha>-<随机>-backend"）。绝不能用服务器 checkout 的 HEAD 顶替：
# 服务器 git 常年落后于线上镜像，用它会把错误的 commit 记成"线上版本"。
built_from_commit="$(printf '%s' "$running_tag" | awk -F- '{print $2}')"
server_checkout_commit="$(git -C "$RECORD_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)"

# 兼容既有部署仪式：这个文件必须保持"只有一行裸 tag"，
# 因为调用方是 export BACKEND_VERSION=$(cat .deploy_version_backend)。
printf '%s\n' "$running_tag" > "${RECORD_DIR}/.deploy_version_backend"

# 细节放旁边的 sidecar，供事故时定位回滚点。时间一律 UTC，
# 避免 tag 里那种"构建机本地时区"导致的跨时区误读。
cat > "${RECORD_DIR}/.deploy_state_backend" <<EOF
tag=${running_tag}
image_id=${running_image_id}
image_created=${image_created}
container_id=${container_id}
container_started=${container_started}
built_from_commit=${built_from_commit}
server_checkout_commit=${server_checkout_commit}
recorded_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
recorded_by=server_deploy_backend.sh
EOF

echo "已记录线上版本: ${running_tag} (image ${running_image_id})"

if [ "$built_from_commit" != "$server_checkout_commit" ]; then
  echo "警告: 线上镜像构建自 commit ${built_from_commit}，但本机 checkout 停在 ${server_checkout_commit}。" >&2
  echo "      服务器上的代码不等于线上跑的代码，排查问题时以 ${built_from_commit} 为准。" >&2
fi

# ---------------------------------------------------------------------------
# $QUANTY_ENV_FILE 示例（默认 /etc/quanty/backend.env，权限必须 600）：
#
#   sudo mkdir -p /etc/quanty
#   sudo install -m 600 /dev/null /etc/quanty/backend.env
#   sudo vi /etc/quanty/backend.env
#
#   DB_PASS=...
#   REDIS_PASSWORD=...
#   BINANCE_API_KEY=...
#   BINANCE_API_SECRET=...
#   JWT_SECRET=...                # 换掉会让所有已登录用户被踢下线，需重新登录
#   CONFIG_ENCRYPTION_KEY=...     # 【切勿更换】DB 里 users.configs 是用它加密的，
#                                 # 换了以后所有用户的交易所配置将无法解密
#   ADMIN_PASSWORD=...            # 可选
#   OPENROUTER_API_KEY=...        # 可选，自动调参用
#   LARK_WEBHOOK_URL=...          # 可选，飞书告警
#   LARK_SECRET=...               # 可选
#
# 值含空格或特殊字符时要加引号（本文件是被 shell source 的）。
# 做市侧的 MM_GATE_* / MM_HL_* 走的是另一个文件：/root/work/quanty_trade/.env
# （已被 .gitignore 忽略，通过 --env-file 注入容器）。
# ---------------------------------------------------------------------------
