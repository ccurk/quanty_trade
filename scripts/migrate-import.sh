#!/bin/bash
# ============================================================================
# QuantyTrade 完整迁移 IMPORT 脚本
# ----------------------------------------------------------------------------
# 在【新服务器】上跑。恢复 migrate-export.sh 生成的 tar.gz 包：
#   1. 自动备份当前 DB（防止覆盖现有数据）
#   2. 还原全量 SQL（覆盖现有所有表）
#   3. 还原配置文件 + 加密密钥
#   4. 还原 strategies/ 目录
#   5. 触发 docker-compose 重建 backend
#
# 用法：
#   cd /root/work/quanty_trade
#   bash scripts/migrate-import.sh /tmp/quanty_migration_20260101_120000.tar.gz
#
# ⚠️  这会覆盖新服务器现有数据库内容。强烈建议先备份。
# ============================================================================

set -e

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

PKG="$1"
if [ -z "$PKG" ] || [ ! -f "$PKG" ]; then
  echo "❌ 用法: $0 <migration.tar.gz>"
  exit 1
fi

echo "════════════════════════════════════════════════════"
echo "QuantyTrade Migration IMPORT"
echo "════════════════════════════════════════════════════"
echo "包: $PKG"
echo "目标项目: $REPO_ROOT"
echo ""

# ─── 确认 ───
read -p "⚠️  这会覆盖现有数据库内容。继续？(yes/no): " CONFIRM
if [ "$CONFIRM" != "yes" ]; then
  echo "已取消"
  exit 0
fi

WORK=$(mktemp -d)
tar xzf "$PKG" -C "$WORK"
echo "📦 解包到 $WORK"

if [ -f "$WORK/MANIFEST.txt" ]; then
  echo ""
  echo "─── 包内容 ───"
  cat "$WORK/MANIFEST.txt"
  echo "─────────────"
  echo ""
fi

# ─── 从新服务器的 conf 读 DB（如果没有就从包里读） ───
USE_CONF="$REPO_ROOT/conf/conf_pro.yaml"
if [ ! -f "$USE_CONF" ]; then
  if [ -f "$WORK/conf/conf_pro.yaml" ]; then
    USE_CONF="$WORK/conf/conf_pro.yaml"
    echo "ℹ️  新服务器没 conf_pro.yaml，使用包里的"
  else
    echo "❌ 找不到任何 conf_pro.yaml"
    exit 1
  fi
fi

DB_HOST=$(awk '/^db:/{f=1;next} f && /^[^ ]/{exit} f && /host:/{gsub(/[" ]/,"",$2); print $2; exit}' "$USE_CONF")
DB_PORT=$(awk '/^db:/{f=1;next} f && /^[^ ]/{exit} f && /port:/{gsub(/[" ]/,"",$2); print $2; exit}' "$USE_CONF")
DB_USER=$(awk '/^db:/{f=1;next} f && /^[^ ]/{exit} f && /user:/{gsub(/[" ]/,"",$2); print $2; exit}' "$USE_CONF")
DB_PASS=$(awk '/^db:/{f=1;next} f && /^[^ ]/{exit} f && /pass:/{gsub(/[" ]/,"",$2); print $2; exit}' "$USE_CONF")
DB_NAME=$(awk '/^db:/{f=1;next} f && /^[^ ]/{exit} f && /name:/{gsub(/[" ]/,"",$2); print $2; exit}' "$USE_CONF")

echo "📊 目标 DB: ${DB_USER}@${DB_HOST}:${DB_PORT}/${DB_NAME}"
echo ""

# ───────────────────────────────────────────────────────────────────────
# Step 1: 备份当前 DB（再保险一次）
# ───────────────────────────────────────────────────────────────────────
echo "🛡  1/4 备份新服务器当前 DB..."
BACKUP="/tmp/before_import_${DB_NAME}_$(date +%s).sql"
mysqldump \
  --host="$DB_HOST" --port="$DB_PORT" \
  --user="$DB_USER" --password="$DB_PASS" \
  --single-transaction --no-tablespaces \
  "$DB_NAME" > "$BACKUP" 2>/dev/null || {
    echo "   ⚠️  备份失败（可能 DB 是空的，第一次部署），继续"
  }
[ -s "$BACKUP" ] && echo "   ✅ 备份: $BACKUP ($(du -h $BACKUP | cut -f1))"

# ───────────────────────────────────────────────────────────────────────
# Step 2: 还原 SQL
# ───────────────────────────────────────────────────────────────────────
if [ ! -f "$WORK/db.sql" ]; then
  echo "❌ 包里没找到 db.sql"
  rm -rf "$WORK"
  exit 1
fi
echo "🗄  2/4 还原 SQL 到 $DB_NAME..."
mysql \
  --host="$DB_HOST" --port="$DB_PORT" \
  --user="$DB_USER" --password="$DB_PASS" \
  --default-character-set=utf8mb4 \
  "$DB_NAME" < "$WORK/db.sql" 2> "$WORK/restore.err" || {
    echo "❌ SQL 还原失败:"
    head -20 "$WORK/restore.err"
    echo ""
    echo "可以用备份恢复: mysql ... $DB_NAME < $BACKUP"
    exit 1
  }
echo "   ✅ DB 还原完成"

# ───────────────────────────────────────────────────────────────────────
# Step 3: 还原配置文件
# ───────────────────────────────────────────────────────────────────────
echo "🔐 3/4 还原配置..."
mkdir -p conf
if [ -d "$WORK/conf" ]; then
  # 备份当前 conf
  for yml in conf/conf_pro.yaml conf/conf_dev.yaml; do
    if [ -f "$yml" ]; then
      cp "$yml" "${yml}.bak.$(date +%s)"
      echo "   备份 $yml"
    fi
  done
  cp "$WORK/conf/"*.yaml conf/ 2>/dev/null && echo "   ✅ conf/*.yaml"
fi
for env in .env config.env; do
  if [ -f "$WORK/$env" ]; then
    [ -f "$env" ] && cp "$env" "${env}.bak.$(date +%s)"
    cp "$WORK/$env" .
    echo "   ✅ $env"
  fi
done

# ───────────────────────────────────────────────────────────────────────
# Step 4: 还原 strategies/
# ───────────────────────────────────────────────────────────────────────
echo "🐍 4/4 还原 strategies/..."
if [ -d "$WORK/strategies" ]; then
  mkdir -p strategies
  # 备份现有
  if [ -n "$(ls strategies/*.py 2>/dev/null)" ]; then
    mkdir -p strategies/_pre_import_backup
    mv strategies/*.py strategies/_pre_import_backup/ 2>/dev/null || true
    echo "   现有 .py 移到 strategies/_pre_import_backup/"
  fi
  cp "$WORK/strategies/"*.py strategies/ 2>/dev/null && \
    echo "   ✅ $(ls strategies/*.py | wc -l) 个策略文件"
fi

# ───────────────────────────────────────────────────────────────────────
# Step 5: 重启服务
# ───────────────────────────────────────────────────────────────────────
echo ""
echo "🔄 重启 backend container..."
if command -v docker-compose > /dev/null 2>&1; then
  docker-compose restart backend 2>&1 | tail -10
elif command -v docker > /dev/null 2>&1 && docker ps --format '{{.Names}}' | grep -q quanty; then
  docker restart $(docker ps --format '{{.Names}}' | grep quanty) 2>&1 | tail -10
else
  echo "   ⚠️  没找到 docker-compose 或 docker，请手动重启"
fi

sleep 5

echo ""
echo "════════════════════════════════════════════════════"
echo "✅ 迁移完成"
echo "════════════════════════════════════════════════════"
echo ""
echo "立刻检查："
echo "  1. docker logs --tail 30 \$(docker ps --format '{{.Names}}' | grep backend)"
echo "  2. 浏览器打开新服务器域名"
echo "  3. 看 strategy 列表 / 历史持仓 / PnL 日历是否完整"
echo ""
echo "失败回滚："
[ -s "$BACKUP" ] && \
echo "  mysql ... $DB_NAME < $BACKUP   # 回滚 DB"
echo "  (conf 和 strategies 的 .bak 文件在原位置)"
echo ""
echo "完成后清理："
echo "  rm $PKG $BACKUP"
echo "════════════════════════════════════════════════════"

rm -rf "$WORK"
