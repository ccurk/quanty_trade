#!/bin/bash
# ============================================================================
# QuantyTrade 加密备份脚本
# ----------------------------------------------------------------------------
# 流程：mysqldump → gzip → gpg 对称加密 → rclone 上传 → 失败 TG 告警
#
# 依赖（host 必须装）：mysqldump、gzip、gpg、rclone、curl
#
# 配置（环境变量，建议放 /etc/quanty-backup.env 0600 root:root）：
#   BACKUP_PASSPHRASE  — gpg 加密口令（重要！丢了备份废了）
#   RCLONE_REMOTE      — rclone remote 名（如 r2、b2、s3）
#   RCLONE_BUCKET      — 桶名（如 quanty-backups）
#   TG_TOKEN / TG_CHAT — TG 通知（可选；不配就不发）
#   DB_HOST/PORT/USER/PASS/NAME — DB 连接，默认从 conf/conf_pro.yaml 读
#
# 用法：
#   bash scripts/db-backup.sh hourly   # 每小时（保留 7 天）
#   bash scripts/db-backup.sh daily    # 每日（保留 30 天）
#   bash scripts/db-backup.sh weekly   # 每周（保留 12 周）
# ============================================================================

set -uo pipefail

MODE="${1:-daily}"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

# ─── 加载配置 ───
ENV_FILE="${QUANTY_BACKUP_ENV:-/etc/quanty-backup.env}"
[ -f "$ENV_FILE" ] && source "$ENV_FILE"

CONF="${QUANTY_CONF:-conf/conf_pro.yaml}"
if [ -f "$CONF" ]; then
  DB_HOST="${DB_HOST:-$(awk '/^db:/{f=1;next} f && /^[^ ]/{exit} f && /host:/{gsub(/[" ]/,"",$2); print $2; exit}' "$CONF")}"
  DB_PORT="${DB_PORT:-$(awk '/^db:/{f=1;next} f && /^[^ ]/{exit} f && /port:/{gsub(/[" ]/,"",$2); print $2; exit}' "$CONF")}"
  DB_USER="${DB_USER:-$(awk '/^db:/{f=1;next} f && /^[^ ]/{exit} f && /user:/{gsub(/[" ]/,"",$2); print $2; exit}' "$CONF")}"
  DB_PASS="${DB_PASS:-$(awk '/^db:/{f=1;next} f && /^[^ ]/{exit} f && /pass:/{gsub(/[" ]/,"",$2); print $2; exit}' "$CONF")}"
  DB_NAME="${DB_NAME:-$(awk '/^db:/{f=1;next} f && /^[^ ]/{exit} f && /name:/{gsub(/[" ]/,"",$2); print $2; exit}' "$CONF")}"
fi

# ─── 必备检查 ───
fatal() {
  local msg="$1"
  echo "❌ $msg" >&2
  if [ -n "${TG_TOKEN:-}" ] && [ -n "${TG_CHAT:-}" ]; then
    curl -s --max-time 15 -X POST "https://api.telegram.org/bot${TG_TOKEN}/sendMessage" \
      -d "chat_id=${TG_CHAT}" --data-urlencode "text=❌ QuantyTrade 备份失败 [${MODE}]: ${msg}" \
      > /dev/null || true
  fi
  exit 1
}

[ -z "${BACKUP_PASSPHRASE:-}" ] && fatal "BACKUP_PASSPHRASE 未配置（$ENV_FILE 里加）"
[ -z "${RCLONE_REMOTE:-}" ]     && fatal "RCLONE_REMOTE 未配置"
[ -z "${RCLONE_BUCKET:-}" ]     && fatal "RCLONE_BUCKET 未配置"
[ -z "${DB_HOST:-}" ]           && fatal "DB_HOST 未配置（yaml 读不到，请显式设）"

for cmd in mysqldump gzip gpg rclone curl; do
  command -v "$cmd" > /dev/null 2>&1 || fatal "缺少命令: $cmd（用 apt/brew 装）"
done

# ─── 路径 + 时间戳 ───
TS=$(date +%Y%m%d_%H%M%S)
DATE_DIR=$(date +%Y/%m/%d)
TMP=$(mktemp -d)
trap "rm -rf $TMP" EXIT

NAME_BASE="quanty_${MODE}_${TS}"
DUMP_RAW="$TMP/${NAME_BASE}.sql"
DUMP_GZ="$TMP/${NAME_BASE}.sql.gz"
DUMP_ENC="$TMP/${NAME_BASE}.sql.gz.gpg"

echo "═══════════════════════════════════════"
echo "QuantyTrade DB 备份 [${MODE}] @ ${TS}"
echo "═══════════════════════════════════════"
echo "DB:     ${DB_USER}@${DB_HOST}:${DB_PORT}/${DB_NAME}"
echo "Remote: ${RCLONE_REMOTE}:${RCLONE_BUCKET}/${MODE}/${DATE_DIR}/"
echo ""

# ─── Step 1: mysqldump ───
echo "🗄  1/4 mysqldump..."
START_T1=$(date +%s)
mysqldump \
  --host="$DB_HOST" --port="$DB_PORT" \
  --user="$DB_USER" --password="$DB_PASS" \
  --single-transaction \
  --routines --triggers --events \
  --default-character-set=utf8mb4 \
  --no-tablespaces \
  "$DB_NAME" > "$DUMP_RAW" 2> "$TMP/dump.err" || {
    fatal "mysqldump 失败: $(head -5 $TMP/dump.err | tr '\n' ' ')"
  }
RAW_SIZE=$(stat -c%s "$DUMP_RAW" 2>/dev/null || stat -f%z "$DUMP_RAW")
echo "   ✅ $(numfmt --to=iec $RAW_SIZE 2>/dev/null || echo "${RAW_SIZE}B") 原始大小（耗时 $(($(date +%s) - START_T1))s）"

# ─── Step 2: gzip ───
echo "🗜  2/4 gzip..."
START_T2=$(date +%s)
gzip -9 < "$DUMP_RAW" > "$DUMP_GZ" || fatal "gzip 失败"
GZ_SIZE=$(stat -c%s "$DUMP_GZ" 2>/dev/null || stat -f%z "$DUMP_GZ")
echo "   ✅ $(numfmt --to=iec $GZ_SIZE 2>/dev/null || echo "${GZ_SIZE}B") gzip 后（耗时 $(($(date +%s) - START_T2))s）"
rm -f "$DUMP_RAW"

# ─── Step 3: gpg 对称加密 ───
echo "🔐 3/4 gpg 加密..."
START_T3=$(date +%s)
gpg --batch --yes --quiet \
  --symmetric --cipher-algo AES256 \
  --passphrase "$BACKUP_PASSPHRASE" \
  --output "$DUMP_ENC" "$DUMP_GZ" || fatal "gpg 加密失败"
ENC_SIZE=$(stat -c%s "$DUMP_ENC" 2>/dev/null || stat -f%z "$DUMP_ENC")
echo "   ✅ $(numfmt --to=iec $ENC_SIZE 2>/dev/null || echo "${ENC_SIZE}B") 加密后（耗时 $(($(date +%s) - START_T3))s）"
rm -f "$DUMP_GZ"

# ─── Step 4: rclone 上传 ───
echo "☁️  4/4 上传到 ${RCLONE_REMOTE}..."
START_T4=$(date +%s)
REMOTE_PATH="${RCLONE_REMOTE}:${RCLONE_BUCKET}/${MODE}/${DATE_DIR}/${NAME_BASE}.sql.gz.gpg"
rclone copyto "$DUMP_ENC" "$REMOTE_PATH" \
  --transfers 1 --retries 3 --low-level-retries 5 \
  --s3-upload-cutoff 50M --buffer-size 16M 2> "$TMP/up.err" || {
    fatal "rclone 上传失败: $(head -5 $TMP/up.err | tr '\n' ' ')"
  }
echo "   ✅ 上传完成（耗时 $(($(date +%s) - START_T4))s）"
echo "   📍 $REMOTE_PATH"

# ─── 清理老备份（按 MODE 不同的保留策略）───
echo ""
echo "🧹 清理过期备份..."
case "$MODE" in
  hourly)  RETAIN_DAYS=7 ;;
  daily)   RETAIN_DAYS=30 ;;
  weekly)  RETAIN_DAYS=84 ;;
  *)       RETAIN_DAYS=30 ;;
esac

CUTOFF=$(date -d "${RETAIN_DAYS} days ago" +%Y-%m-%d 2>/dev/null \
       || date -v-${RETAIN_DAYS}d +%Y-%m-%d 2>/dev/null \
       || echo "")
if [ -n "$CUTOFF" ]; then
  PURGED=$(rclone delete "${RCLONE_REMOTE}:${RCLONE_BUCKET}/${MODE}/" \
    --min-age "${RETAIN_DAYS}d" \
    --rmdirs 2>&1 | grep -cE "Deleted|removed" || true)
  echo "   ✅ 已清理 ${PURGED} 个超 ${RETAIN_DAYS} 天的备份"
fi

# ─── 通知 TG（成功）───
TOTAL_T=$(($(date +%s) - START_T1))
SUMMARY="✅ QuantyTrade 备份成功 [${MODE}]
时间: ${TS}
原始: $(numfmt --to=iec $RAW_SIZE 2>/dev/null || echo "${RAW_SIZE}B")
加密后: $(numfmt --to=iec $ENC_SIZE 2>/dev/null || echo "${ENC_SIZE}B")
耗时: ${TOTAL_T}s
路径: ${MODE}/${DATE_DIR}/${NAME_BASE}.sql.gz.gpg"

echo ""
echo "═══════════════════════════════════════"
echo "$SUMMARY"
echo "═══════════════════════════════════════"

if [ -n "${TG_TOKEN:-}" ] && [ -n "${TG_CHAT:-}" ]; then
  curl -s --max-time 15 -X POST "https://api.telegram.org/bot${TG_TOKEN}/sendMessage" \
    -d "chat_id=${TG_CHAT}" --data-urlencode "text=${SUMMARY}" \
    > /dev/null || echo "warn: TG 通知发送失败"
fi
