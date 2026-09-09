#!/bin/bash
# ============================================================================
# QuantyTrade 完整迁移 EXPORT 脚本
# ----------------------------------------------------------------------------
# 在【旧服务器】上跑。导出：
#   1. MySQL 全量 SQL dump（所有表，包含交易/PnL/审计历史）
#   2. 配置文件 conf/conf_pro.yaml（含加密密钥 — 必须！）
#   3. strategies/ 目录（Python 源文件）
#   4. （可选）Redis 全量快照（含 cooldown 状态）
#
# 跑完会生成一个 tar.gz 包，scp 到新服务器后用 migrate-import.sh 恢复。
#
# 用法：
#   cd /root/work/quanty_trade
#   bash scripts/migrate-export.sh
#
# 输出：/tmp/quanty_migration_YYYYMMDD_HHMMSS.tar.gz
# ============================================================================

set -e

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

TIMESTAMP=$(date +%Y%m%d_%H%M%S)
OUT_TGZ="/tmp/quanty_migration_${TIMESTAMP}.tar.gz"
WORK=$(mktemp -d)
echo "📦 工作目录: $WORK"

# ─── 从 yaml 读 DB 配置（不依赖 yq 等外部工具，纯 grep）───
CONF="conf/conf_pro.yaml"
if [ ! -f "$CONF" ]; then
  echo "❌ 找不到 $CONF —— 你是不是不在项目根目录？"
  exit 1
fi

# 简易 YAML 解析：找 db: block 后面 4 行
DB_HOST=$(awk '/^db:/{f=1;next} f && /^[^ ]/{exit} f && /host:/{gsub(/[" ]/,"",$2); print $2; exit}' "$CONF")
DB_PORT=$(awk '/^db:/{f=1;next} f && /^[^ ]/{exit} f && /port:/{gsub(/[" ]/,"",$2); print $2; exit}' "$CONF")
DB_NAME=$(awk '/^db:/{f=1;next} f && /^[^ ]/{exit} f && /name:/{gsub(/[" ]/,"",$2); print $2; exit}' "$CONF")

# 凭据不从 yaml 读：db.pass 恒为 ""（口令一律不入库），读出来是空口令，
# 会在下面 Step 1 的全量 mysqldump 处失败。本脚本导出整库，用 root（口令 A）。
DB_USER="${DB_USER:-}"
DB_PASS="${DB_PASS:-}"
if [ -z "$DB_USER" ] || [ -z "$DB_PASS" ]; then
  echo "❌ 请先 export DB_USER / DB_PASS（MySQL root，口令 A）再跑本脚本。"
  echo "   例：set -a; . /etc/quanty-backup.env; set +a; bash scripts/migrate-export.sh"
  exit 1
fi

echo "📊 DB: ${DB_USER}@${DB_HOST}:${DB_PORT}/${DB_NAME}"
echo ""

# ───────────────────────────────────────────────────────────────────────
# Step 1: SQL dump（完整 DB，含所有历史 — 这是最重要的一步）
# ───────────────────────────────────────────────────────────────────────
# ─── 内容校验清单：**必须在 dump 之前取** ───
# 只对行数的验收会漏判：2026-09-09 恢复演练实测 strategy_positions 2815=2815、
# daily_pn_ls 1016=1016，行数完美对上、内容却是差的（RUNBOOK-restore.md §8.3）。
# 所以这里逐表记下 (id 上界, 行数, 顺序无关的内容校验和)，由 migrate-import.sh 复算比对。
#
# 顺序为什么是「先清单、后 dump」：清单里的 id 上界取自这一刻的 MAX(id)。
# 之后到 dump 完成之间源库新写的行，id 都大于上界，两边一起被排除掉 —— 不会误报。
# 反过来先 dump 后取清单，那些新行会进上界却不在包里，每一次导入都报不一致，
# 而一条永远报警的验收比没有验收更坏。
echo "🔎 0/4 内容校验清单（先于 dump 取，见上面注释）..."
if bash "$REPO_ROOT/scripts/db-content-checks.sh" \
      "$DB_HOST" "$DB_PORT" "$DB_NAME" "$DB_USER" "$DB_PASS" \
      > "$WORK/db.checks.tsv" 2> "$WORK/checks.err"; then
  echo "   ✅ $(wc -l < "$WORK/db.checks.tsv" | tr -d ' ') 张表已记下行数 + 内容校验和"
else
  echo "❌ 内容校验清单生成失败 —— 不导出一个「事后无法验收」的迁移包:"
  sed -n '1,10p' "$WORK/checks.err"
  rm -rf "$WORK"
  exit 1
fi

echo "🗄  1/4 mysqldump 全量 DB（含交易历史、PnL、审计）..."
mysqldump \
  --host="$DB_HOST" \
  --port="$DB_PORT" \
  --user="$DB_USER" \
  --password="$DB_PASS" \
  --single-transaction \
  --routines --triggers --events \
  --default-character-set=utf8mb4 \
  --no-tablespaces \
  "$DB_NAME" > "$WORK/db.sql" 2> "$WORK/dump.err" || {
    echo "❌ mysqldump 失败:"
    cat "$WORK/dump.err"
    rm -rf "$WORK"
    exit 1
  }
SQL_SIZE=$(du -h "$WORK/db.sql" | cut -f1)
echo "   ✅ $SQL_SIZE → db.sql"

# ───────────────────────────────────────────────────────────────────────
# Step 2: 配置文件 + 加密密钥（必须，不然 binance API key 解不出来）
# ───────────────────────────────────────────────────────────────────────
echo "🔐 2/4 配置文件 + 加密密钥..."
mkdir -p "$WORK/conf"
# 复制 prod / dev 两个 yaml（如有）
for yml in conf/conf_pro.yaml conf/conf_dev.yaml; do
  [ -f "$yml" ] && cp "$yml" "$WORK/conf/" && echo "   ✅ $yml"
done

# 如果有 .env 也带上
[ -f ".env" ] && cp .env "$WORK/.env" && echo "   ✅ .env"
[ -f "config.env" ] && cp config.env "$WORK/config.env" && echo "   ✅ config.env"

# 显式提取 CONFIG_ENCRYPTION_KEY / JWT_SECRET（如果在 env 而不是 yaml）
# 让用户看到必须保留的关键值
echo "   ⚠️  请确认下面 2 个变量都在 conf yaml 或 env 里:"
for v in CONFIG_ENCRYPTION_KEY JWT_SECRET; do
  if grep -rE "^[^#]*${v}" conf/ *.env 2>/dev/null | head -1 > /dev/null; then
    echo "      ✅ $v 存在"
  else
    echo "      ⚠️  $v 未找到 — 可能是 docker-compose env 注入的，记得在新服务器同步"
  fi
done

# ───────────────────────────────────────────────────────────────────────
# Step 3: 策略 Python 文件 + 子目录
# ───────────────────────────────────────────────────────────────────────
echo "🐍 3/4 strategies/ 目录..."
mkdir -p "$WORK/strategies"
# 主目录的 .py
find strategies -maxdepth 1 -name "*.py" -exec cp {} "$WORK/strategies/" \;
# _runtime（运行时生成的脚本，可选；建议不带，让新服务器从 template 生成）
# 但日志/备份带上
if [ -d "logs" ]; then
  mkdir -p "$WORK/logs"
  find logs -maxdepth 1 -name "*.log" -mtime -7 -exec cp {} "$WORK/logs/" \;
  echo "   ✅ 7 天内日志"
fi
PYFILES=$(ls -1 "$WORK/strategies/"*.py 2>/dev/null | wc -l)
echo "   ✅ $PYFILES 个 .py 文件"

# ───────────────────────────────────────────────────────────────────────
# Step 4: Redis 快照（可选 — 含 cooldown 等运行时状态）
# ───────────────────────────────────────────────────────────────────────
echo "📮 4/4 Redis 快照（可选）..."
if command -v redis-cli > /dev/null 2>&1; then
  REDIS_HOST=$(awk '/^redis:/{f=1;next} f && /^[^ ]/{exit} f && /host:/{gsub(/[" ]/,"",$2); print $2; exit}' "$CONF")
  REDIS_PORT=$(awk '/^redis:/{f=1;next} f && /^[^ ]/{exit} f && /port:/{gsub(/[" ]/,"",$2); print $2; exit}' "$CONF")
  REDIS_PASS=$(awk '/^redis:/{f=1;next} f && /^[^ ]/{exit} f && /pass.*:/{gsub(/[" ]/,"",$2); print $2; exit}' "$CONF")
  REDIS_HOST=${REDIS_HOST:-127.0.0.1}
  REDIS_PORT=${REDIS_PORT:-6379}

  if [ -n "$REDIS_PASS" ]; then
    redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" -a "$REDIS_PASS" --no-auth-warning --scan --pattern 'qt:*' \
      | head -50 > "$WORK/redis_keys.txt" 2>/dev/null || true
  else
    redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" --scan --pattern 'qt:*' \
      | head -50 > "$WORK/redis_keys.txt" 2>/dev/null || true
  fi
  KEY_COUNT=$(wc -l < "$WORK/redis_keys.txt" 2>/dev/null || echo 0)
  echo "   ✅ 发现 $KEY_COUNT 个 qt:* keys（仅记录列表，不带值）"
  echo "      cooldown 状态会在新服务器策略首次开仓后重建，不影响"
else
  echo "   ⚠️  没有 redis-cli，跳过 Redis 快照"
fi

# ───────────────────────────────────────────────────────────────────────
# Step 5: 加 MANIFEST + 打包
# ───────────────────────────────────────────────────────────────────────
cat > "$WORK/MANIFEST.txt" <<EOF
QuantyTrade Migration Bundle
=============================
导出时间: $(date -u +%Y-%m-%dT%H:%M:%SZ)
源服务器主机名: $(hostname)
DB: ${DB_USER}@${DB_HOST}:${DB_PORT}/${DB_NAME}
DB dump 大小: $SQL_SIZE
策略文件: $PYFILES

包含的表统计（information_schema.table_rows 是 InnoDB 的**估算值**，不是精确行数，
只当概览看。精确行数 + 内容校验和在包里的 db.checks.tsv，由 migrate-import.sh 复算）:
$(mysql --host="$DB_HOST" --port="$DB_PORT" --user="$DB_USER" --password="$DB_PASS" -BN -e "
  SELECT table_name, table_rows
  FROM information_schema.tables
  WHERE table_schema = '$DB_NAME'
  ORDER BY table_rows DESC
" 2>/dev/null | head -20)

恢复方法:
   scp $(basename "$OUT_TGZ") <new-server>:/tmp/
   ssh <new-server>
   cd /root/work/quanty_trade  # 或目标目录
   bash scripts/migrate-import.sh /tmp/$(basename "$OUT_TGZ")
EOF

echo ""
echo "📦 打包..."
cd "$WORK"
tar czf "$OUT_TGZ" .
cd "$REPO_ROOT"

PKG_SIZE=$(du -h "$OUT_TGZ" | cut -f1)
echo ""
echo "════════════════════════════════════════════════════"
echo "✅ 导出完成"
echo "════════════════════════════════════════════════════"
echo "📦 包路径: $OUT_TGZ"
echo "📊 大小: $PKG_SIZE"
echo ""
echo "下一步："
echo "  scp $OUT_TGZ user@new-server:/tmp/"
echo "  ssh user@new-server"
echo "  cd /root/work/quanty_trade  # 或目标项目目录"
echo "  bash scripts/migrate-import.sh /tmp/$(basename "$OUT_TGZ")"
echo "════════════════════════════════════════════════════"

rm -rf "$WORK"
