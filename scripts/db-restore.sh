#!/bin/bash
# ============================================================================
# ⛔ 已作废（2026-09-09 老徐，台账 #116）。**本脚本会拒绝执行**，往下第一段就 exit 1。
# ----------------------------------------------------------------------------
# 为什么是作废而不是修：
#
#  1. **它从来没有过输入。** 它读的是 db-backup.sh 上传到 rclone remote 的
#     gpg 对称加密包 —— 而那条链路一次都没跑过（台账 #68：服务器上 rclone 没装、
#     /etc/quanty-backup.env 不存在、桶也还没开）。没有输入的恢复脚本不是"坏了"，
#     是还没出生。
#  2. **真正跑过的恢复流程不是它。** 2026-09-09 做过两次真实恢复演练，
#     全过程写在 state/RUNBOOK-restore.md（agent-office 仓库）第 1~8 节，
#     每条命令都真跑过，实测「决定恢复 → 库可查」66.2 秒。以那份为准。
#  3. **异地备份要重做，形状会变。** 待办 #30/#31 的结论：加密必须从
#     gpg --symmetric 改成非对称（对称口令和备份存在同一台机器上，机器没了口令一起没，
#     机器被入侵两样一起被拿走）。所以这个脚本即使接上也要重写。
#  4. **留着它是把上膛的枪放在桌上。** 它 read 一个 yes 就 DROP 掉整个生产库，
#     而下面那句"先备份当前 DB 做安全网"用的是 `|| { 继续 }` —— 备份失败被吞掉，
#     照样往下覆盖。这正是台账 #116 在 migrate-import.sh 里修掉的同一个洞。
#     那个洞在 migrate-import.sh 里值得修（迁移是真要用的路径），在这里不值得：
#     修好了也只是让一个没有输入的脚本失败得体面一点。
#
# 文件保留不删：它是 rclone + gpg 那一段的参考实现，待办 #30/#31 落地时要照着重写。
#
# 原用法（已不可用，仅存档）：
#   bash scripts/db-restore.sh                     # 列出最近 20 个备份让你选
#   bash scripts/db-restore.sh latest              # 自动用最新的 daily
#   bash scripts/db-restore.sh <remote_path>       # 指定路径
# ============================================================================

cat >&2 <<'DEPRECATED'
⛔ scripts/db-restore.sh 已作废，拒绝执行（台账 #116）。

要恢复 quanty_trade，按这个来：
  state/RUNBOOK-restore.md（agent-office 仓库）第 1~8 节
  —— 每条命令都真跑过，实测「决定恢复 → 库可查」66.2 秒。

现在能用的备份是 /var/backups/quanty/ 下 /usr/local/bin/quanty-backup.sh 产出的那份。
本脚本读的 rclone 远端备份还不存在（待办 #30 开桶 / #31 换非对称加密）。
DEPRECATED
exit 1

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
  DB_NAME="${DB_NAME:-$(awk '/^db:/{f=1;next} f && /^[^ ]/{exit} f && /name:/{gsub(/[" ]/,"",$2); print $2; exit}' "$CONF")}"
fi
# DB_USER / DB_PASS 不从 yaml 兜底，理由同 db-backup.sh：口令字段恒为空，
# 兜底得到的是「空口令」，会一路跑到 mysql restore 才失败——而这是恢复流程，
# 那时候通常已经在出事了，不该再多一个看不懂的报错。恢复用 root（口令 A）。
DB_USER="${DB_USER:-}"
DB_PASS="${DB_PASS:-}"

[ -z "${BACKUP_PASSPHRASE:-}" ] && { echo "❌ BACKUP_PASSPHRASE 未配置"; exit 1; }
[ -z "${RCLONE_REMOTE:-}" ]     && { echo "❌ RCLONE_REMOTE 未配置"; exit 1; }
[ -z "${RCLONE_BUCKET:-}" ]     && { echo "❌ RCLONE_BUCKET 未配置"; exit 1; }
[ -z "${DB_USER:-}" ]           && { echo "❌ DB_USER 未配置（$ENV_FILE 里加，恢复走 root）"; exit 1; }
[ -z "${DB_PASS:-}" ]           && { echo "❌ DB_PASS 未配置（$ENV_FILE 里加；要的是 MySQL root 口令）"; exit 1; }

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
