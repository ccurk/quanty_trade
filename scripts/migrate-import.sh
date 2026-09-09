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
DB_NAME=$(awk '/^db:/{f=1;next} f && /^[^ ]/{exit} f && /name:/{gsub(/[" ]/,"",$2); print $2; exit}' "$USE_CONF")

# 凭据不从 yaml 读：conf_pro.yaml 的 db.pass 恒为 ""，读出来是空口令，会一路跑到
# 下面 Step 1 的 mysqldump 才失败——而 Step 1 正是「导入前先备份」那一步。
# （那一步过去会把失败吞掉当成「DB 是空的」、无备份直接往下导；已改成 fail closed，
#   台账 #116。这里仍然要求显式给凭据：让它在第一行就停，比跑到 Step 1 才停更好。）
# 本脚本会 mysqldump + 导入整库，权限对齐备份口径：用 root（口令 A）。
DB_USER="${DB_USER:-}"
DB_PASS="${DB_PASS:-}"
if [ -z "$DB_USER" ] || [ -z "$DB_PASS" ]; then
  echo "❌ 请先 export DB_USER / DB_PASS（MySQL root，口令 A）再跑本脚本。"
  echo "   例：set -a; . /etc/quanty-backup.env; set +a; bash scripts/migrate-import.sh ..."
  exit 1
fi

echo "📊 目标 DB: ${DB_USER}@${DB_HOST}:${DB_PORT}/${DB_NAME}"
echo ""

# ───────────────────────────────────────────────────────────────────────
# Step 1: 备份当前 DB（再保险一次）
# ───────────────────────────────────────────────────────────────────────
echo "🛡  1/4 备份新服务器当前 DB..."
BACKUP="/tmp/before_import_${DB_NAME}_$(date +%s).sql"

# ⚠ 这一步必须 fail closed，别再改回 `|| { 继续 }`（台账 #116）。
# 原来的写法把「备份失败」和「DB 本来就是空的」混成一件事：mysqldump 任何原因失败
# ——连不上、口令错、盘满、权限不够——都被当成「空库」，然后**无备份直接往下导**，
# 而 Step 2 是覆盖式导入。那一刻这台机器上的数据就没有第二份了。
# （实测确认过它真的会继续往下走：`a || { echo; }` 里 a 的失败不触发 set -e，
#  bash 只对 && / || 列表的最后一个命令生效。）
# 正确的分法：先问「目标库到底有没有表」——这个问题失败了就是连不上，直接停；
# 答案是 0 张表才叫空库，才允许跳过备份。
PRE_ERR="$WORK/pre_import.err"
EXISTING_TABLES=$(mysql \
  --host="$DB_HOST" --port="$DB_PORT" \
  --user="$DB_USER" --password="$DB_PASS" \
  -N -B -e "SELECT COUNT(*) FROM information_schema.tables
            WHERE table_schema='$DB_NAME' AND table_type='BASE TABLE'" \
  2> "$PRE_ERR") || {
    echo "❌ 连目标 DB 有几张表都数不出来，停。"
    echo "   不知道目标库里有什么，就不能覆盖它。"
    sed -n '1,10p' "$PRE_ERR"
    rm -rf "$WORK"
    exit 1
  }

if [ "$EXISTING_TABLES" -eq 0 ]; then
  echo "   ℹ️  ${DB_NAME} 里 0 张基表，确认是空库（首次部署），跳过备份"
  BACKUP=""   # 置空：后面的回滚提示不许拿一个不存在的文件糊弄人
else
  mysqldump \
    --host="$DB_HOST" --port="$DB_PORT" \
    --user="$DB_USER" --password="$DB_PASS" \
    --single-transaction --no-tablespaces \
    "$DB_NAME" > "$BACKUP" 2> "$PRE_ERR" || {
      echo "❌ 导入前备份失败，停。目标库有 ${EXISTING_TABLES} 张表，不是空库。"
      echo "   Step 2 是覆盖式导入，没有这份备份就没有回滚路径。"
      sed -n '1,10p' "$PRE_ERR"
      rm -f "$BACKUP"
      rm -rf "$WORK"
      exit 1
    }
  if [ ! -s "$BACKUP" ]; then
    echo "❌ 导入前备份产出 0 字节，停。目标库有 ${EXISTING_TABLES} 张表，不可能备出空文件。"
    sed -n '1,10p' "$PRE_ERR"
    rm -f "$BACKUP"
    rm -rf "$WORK"
    exit 1
  fi
  echo "   ✅ 备份: $BACKUP ($(du -h "$BACKUP" | cut -f1))"
fi

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
    if [ -n "$BACKUP" ]; then
      echo "可以用备份恢复: mysql ... $DB_NAME < $BACKUP"
    else
      echo "（导入前目标库是空的，没有备份可回滚 —— 本来也没东西可丢）"
    fi
    exit 1
  }
echo "   ✅ DB 还原完成"

# ───────────────────────────────────────────────────────────────────────
# Step 2b: 内容验收 —— **不是只对行数**
# ───────────────────────────────────────────────────────────────────────
# 2026-09-09 恢复演练实测（RUNBOOK-restore.md §8.3）：strategy_positions 2815=2815、
# daily_pn_ls 1016=1016，行数完美对上、内容却是差的。**只对行数的验收会判为通过。**
# 这里拿 migrate-export.sh 在 dump 之前记下的逐表 (id上界, 行数, 内容校验和) 复算一遍。
if [ -f "$WORK/db.checks.tsv" ]; then
  echo "🔎 2/4b 内容验收：逐表复算行数 + 内容校验和..."
  AFTER_OK=1
  bash "$REPO_ROOT/scripts/db-content-checks.sh" \
    "$DB_HOST" "$DB_PORT" "$DB_NAME" "$DB_USER" "$DB_PASS" "$WORK/db.checks.tsv" \
    > "$WORK/db.checks.after.tsv" 2> "$WORK/checks.err" || AFTER_OK=0
  if [ "$AFTER_OK" != 1 ]; then
    echo "❌ 内容验收跑不起来 —— 不把「没验」说成「验过了」:"
    sed -n '1,10p' "$WORK/checks.err"
    echo "   数据已经导进去了。人工核对前不要放业务上来。"
    echo "   证据留在 $WORK（本次不清理）"
    exit 1
  fi
  if diff -u "$WORK/db.checks.tsv" "$WORK/db.checks.after.tsv" > "$WORK/checks.diff"; then
    echo "   ✅ $(wc -l < "$WORK/db.checks.tsv" | tr -d ' ') 张表：行数与内容校验和全等"
  else
    echo "❌ 内容验收不通过 —— 导进去的和包里记的不是同一份数据:"
    sed -n '1,40p' "$WORK/checks.diff"
    echo ""
    echo "   先别怀疑校验本身：负零 -0 这个已知坑已经在 db-content-checks.sh 里躲开了"
    echo "   （数值列先归一 IF(col = 0, '0', ...)，见台账 #155）。"
    echo "   真正会撞上的一种非故障情形：导出期间源库那边有行被删（比如 05:00 的"
    echo "   db-log-retention.sh 清 api_logs）。那不是假警报，是源库在导出窗口里真删了行 ——"
    echo "   重跑一次 migrate-export.sh 即可。除此之外，一律按数据不一致处理。"
    if [ -n "$BACKUP" ]; then
      echo ""
      echo "   回滚: mysql ... $DB_NAME < $BACKUP"
    fi
    echo "   证据留在 $WORK（本次不清理）"
    exit 1
  fi
else
  echo "⚠️  包里没有 db.checks.tsv（旧版 migrate-export.sh 导出的包）。"
  echo "   本次导入**没有做内容验收** —— 行数看着对不等于数据一样，别当成验过了。"
fi

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
