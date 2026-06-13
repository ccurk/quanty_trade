#!/bin/bash
# ============================================================================
# QuantyTrade 备份恢复脚本
# ----------------------------------------------------------------------------
# 从 R2/B2/S3 拉一个加密备份 → gpg 解密 → gunzip → mysql restore
#
# 用法：
#   bash scripts/db-restore.sh                     # 列出最近 20 个备份让你选
#   bash scripts/db-restore.sh latest              # 自动用最新的 daily
#   bash scripts/db-restore.sh <remote_path>       # 指定路径
#
# 例：
#   bash scripts/db-restore.sh daily/2026/06/12/quanty_daily_20260612_023000.sql.gz.gpg
# ============================================================================

set -uo pipefail

TARGET="${1:-}"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

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

[ -z "${BACKUP_PASSPHRASE:-}" ] && { echo "❌ BACKUP_PASSPHRASE 未配置"; exit 1; }
[ -z "${RCLONE_REMOTE:-}" ]     && { echo "❌ RCLONE_REMOTE 未配置"; exit 1; }
[ -z "${RCLONE_BUCKET:-}" ]     && { echo "❌ RCLONE_BUCKET 未配置"; exit 1; }

echo "═══════════════════════════════════════"
echo "QuantyTrade 备份恢复"
echo "═══════════════════════════════════════"
echo "目标 DB: ${DB_USER}@${DB_HOST}:${DB_PORT}/${DB_NAME}"
echo "Remote:  ${RCLONE_REMOTE}:${RCLONE_BUCKET}"
echo ""

# ─── 处理 TARGET ───
case "$TARGET" in
  "")
    echo "📋 最近 20 个备份："
    rclone lsf "${RCLONE_REMOTE}:${RCLONE_BUCKET}/" -R \
      --include "*.sql.gz.gpg" \
      --files-only 2>/dev/null | sort -r | head -20 | nl
    echo ""
    echo "用法: bash $0 <上面的路径>"
    echo "      bash $0 latest"
    exit 0
    ;;
  latest)
    echo "🔍 查找最新 daily 备份..."
    TARGET=$(rclone lsf "${RCLONE_REMOTE}:${RCLONE_BUCKET}/daily/" -R \
      --include "*.sql.gz.gpg" --files-only 2>/dev/null | sort -r | head -1)
    if [ -z "$TARGET" ]; then
      echo "❌ daily/ 下没找到任何备份，试试 hourly：「bash $0 hourly_latest」"
      exit 1
    fi
    TARGET="daily/${TARGET}"
    echo "   ✅ 锁定: $TARGET"
    ;;
esac

# ─── 确认 ───
echo ""
echo "⚠️  这会覆盖 ${DB_NAME} 数据库现有内容"
read -p "继续？(yes/no): " CONFIRM
[ "$CONFIRM" != "yes" ] && { echo "已取消"; exit 0; }

# ─── 自动备份当前 DB 做安全网 ───
TS=$(date +%Y%m%d_%H%M%S)
SAFETY_BACKUP="/tmp/quanty_pre_restore_${TS}.sql"
echo "🛡  先备份当前 DB 到 $SAFETY_BACKUP（万一恢复失败可以回滚）..."
mysqldump \
  --host="$DB_HOST" --port="$DB_PORT" \
  --user="$DB_USER" --password="$DB_PASS" \
  --single-transaction --no-tablespaces \
  "$DB_NAME" > "$SAFETY_BACKUP" 2>/dev/null || {
    echo "   ⚠️  当前 DB 备份失败（可能是空 DB），继续"
}
[ -s "$SAFETY_BACKUP" ] && echo "   ✅ $(du -h $SAFETY_BACKUP | cut -f1)"

# ─── 下载 ───
TMP=$(mktemp -d)
trap "rm -rf $TMP" EXIT

REMOTE="${RCLONE_REMOTE}:${RCLONE_BUCKET}/${TARGET}"
LOCAL_ENC="$TMP/backup.sql.gz.gpg"

echo "☁️  下载 $REMOTE..."
rclone copyto "$REMOTE" "$LOCAL_ENC" --transfers 1 --retries 3 || {
  echo "❌ rclone 下载失败"
  exit 1
}
echo "   ✅ $(du -h $LOCAL_ENC | cut -f1)"

# ─── 解密 ───
echo "🔓 解密..."
gpg --batch --quiet \
  --passphrase "$BACKUP_PASSPHRASE" \
  --decrypt "$LOCAL_ENC" 2>/dev/null > "$TMP/backup.sql.gz" || {
    echo "❌ gpg 解密失败（口令错？BACKUP_PASSPHRASE 必须和当时备份时一致）"
    exit 1
}
echo "   ✅ $(du -h $TMP/backup.sql.gz | cut -f1) gzip 大小"
rm -f "$LOCAL_ENC"

# ─── gunzip + 还原 ───
echo "🗄  解压 + restore..."
gunzip "$TMP/backup.sql.gz"

mysql \
  --host="$DB_HOST" --port="$DB_PORT" \
  --user="$DB_USER" --password="$DB_PASS" \
  --default-character-set=utf8mb4 \
  "$DB_NAME" < "$TMP/backup.sql" 2> "$TMP/restore.err" || {
    echo "❌ mysql restore 失败:"
    head -20 "$TMP/restore.err"
    echo ""
    [ -s "$SAFETY_BACKUP" ] && \
    echo "可以回滚: mysql ... $DB_NAME < $SAFETY_BACKUP"
    exit 1
}

# ─── 总结 ───
echo ""
echo "═══════════════════════════════════════"
echo "✅ 恢复完成"
echo "═══════════════════════════════════════"
echo "源备份: $TARGET"
echo "目标 DB: $DB_NAME"
echo ""
echo "立刻验证："
echo "  mysql -h $DB_HOST -P $DB_PORT -u $DB_USER -p $DB_NAME -e 'SHOW TABLES;'"
echo "  docker-compose restart backend"
echo ""
echo "如果有问题想回滚到恢复前状态："
[ -s "$SAFETY_BACKUP" ] && \
echo "  mysql ... $DB_NAME < $SAFETY_BACKUP"
echo "  rm $SAFETY_BACKUP   # 验证 OK 后清理"
echo "═══════════════════════════════════════"
