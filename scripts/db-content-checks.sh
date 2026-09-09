#!/usr/bin/env bash
# ============================================================================
# db-content-checks.sh —— 逐表打印「行数 + 内容校验和」，给迁移/恢复做**内容级**验收
# ----------------------------------------------------------------------------
# 为什么不是只对行数：2026-09-09 恢复演练实测（RUNBOOK-restore.md §8.3），
# strategy_positions 2815=2815、daily_pn_ls 1016=1016 —— 行数完美对上，内容却是差的。
# **只对行数的验收会把这两张表判为通过。**
#
# 用法：
#   bash scripts/db-content-checks.sh <host> <port> <db> <user> <pass> [bounds.tsv]
#
# 输出（TSV，按表名排序，可以直接 diff 两份）：
#   <表名>\t<id上界>\t<行数>\t<xor校验和>\t<sum校验和>
#
# 不给 bounds.tsv：库里每张基表都算，各自取 MAX(id) 当上界，上界打进输出。
# 给了 bounds.tsv：**只算 bounds 里列的那些表**，并且用文件里记下的上界。
#                  上界必须沿用导出侧的，否则源库在导出之后新写的行会让每一次验收都不一致。
#                  目标库多出来的表不参与比对（要回答的是「包里的表有没有原样落地」，
#                  不是「两边表集合是否相同」）；bounds 里有而目标库没有 = 失败。
#
# 全程只读：一个 START TRANSACTION WITH CONSISTENT SNAPSHOT 里的 SELECT，
# 不加表锁、不写任何东西。
#
# ── 改这个文件前先读这四条，都是实测踩出来的 ──
#
# 1. **负零 -0**（台账 #155）。mysqldump 写出来的就是 `-0`，丢在**导入**这一步：
#    MySQL 的 SQL 解析器把字面量 -0 归一成 +0（生产那行是 Go 走二进制协议写的）。
#    业务影响为零，但会让内容校验和**对那一行永久报不一致** —— 而一条永远报警的
#    验收比没有验收更坏：下个人会习惯「总有一行对不上」，从此不再看验收结果。
#    所以数值列一律先过 `IF(col = 0, '0', …)`：`-0 = 0` 在 SQL 里为真，
#    两边都归一成字面量 '0'。**它只会合并「和 0 相等」的值，盖不掉任何真实差异。**
#    ⚠ 这个归一**必须按列类型放行**：MySQL 里 `'abc' = 0` 也为真，
#    要是对字符串列也这么干，所有非数字文本会被一起压成 '0'，校验和就废了。
#
# 2. **NULL 哨兵**。CONCAT_WS 会跳过 NULL，('a',NULL,'b') 和 ('a','b') 会算成同一行。
#    每列先 IFNULL(…, CHAR(2))。
#
# 3. **GROUP_CONCAT 默认 1024 字节静默截断**。这个库 231 列，不抬 group_concat_max_len，
#    生成出来的 SQL 会被截断 —— 而截断之后两边照样相等，是假阳性
#    （同一条教训在 RUNBOOK §7 用 GROUP_CONCAT 做整表哈希时已经踩过一次）。
#
# 4. **上界用 id、不用时间戳**。id 单调递增，免时区、免时钟漂移；
#    updated_at 会被业务回写，用它做上界会把「正常的当日回写」判成数据损坏。
# ============================================================================

set -uo pipefail

if [ "$#" -lt 5 ]; then
  echo "用法: $0 <host> <port> <db> <user> <pass> [bounds.tsv]" >&2
  exit 2
fi

DB_HOST="$1"; DB_PORT="$2"; DB_NAME="$3"; DB_USER="$4"; DB_PASS="$5"
BOUNDS_FILE="${6:-}"

command -v mysql > /dev/null 2>&1 || { echo "❌ 缺少 mysql 客户端" >&2; exit 2; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# 口令走 MYSQL_PWD，不走 -p<pass>：后者会出现在进程列表里
# （和 /usr/local/bin/quanty-backup.sh 同一套写法）。
export MYSQL_PWD="$DB_PASS"
MYSQL_ARGS=(--host="$DB_HOST" --port="$DB_PORT" --user="$DB_USER"
            --default-character-set=utf8mb4 --batch --skip-column-names --raw)

# ─── 每张表的上界：给了 bounds.tsv 就用文件里的，否则用 MAX(id) ───
bound_expr() {  # $1 = 表名
  if [ -n "$BOUNDS_FILE" ]; then
    local b
    b="$(awk -F'\t' -v t="$1" '$1==t {print $2; exit}' "$BOUNDS_FILE")"
    if [ -z "$b" ]; then echo "__MISSING__"; return; fi
    printf '%s' "$b"
  else
    printf 'IFNULL((SELECT MAX(`id`) FROM `%s`),0)' "$1"
  fi
}

# ─── 第 1 趟：让数据库自己按 information_schema 生成校验 SQL ───
cat > "$TMP/gen.sql" <<SQL
SET SESSION group_concat_max_len = 16777216;
SELECT CONCAT(c.table_name, '\t', GROUP_CONCAT(
         CASE
           WHEN c.data_type IN ('tinyint','smallint','mediumint','int','integer',
                                'bigint','decimal','numeric','float','double','bit')
             THEN CONCAT('IFNULL(IF(\`', c.column_name, '\` = 0, ''0'', CAST(\`',
                         c.column_name, '\` AS CHAR)), CHAR(2))')
           WHEN c.data_type IN ('binary','varbinary','tinyblob','blob',
                                'mediumblob','longblob')
             THEN CONCAT('IFNULL(HEX(\`', c.column_name, '\`), CHAR(2))')
           ELSE
             CONCAT('IFNULL(CAST(\`', c.column_name, '\` AS CHAR), CHAR(2))')
         END
         ORDER BY c.ordinal_position SEPARATOR ', '))
FROM information_schema.columns c
JOIN information_schema.tables t
  ON t.table_schema = c.table_schema AND t.table_name = c.table_name
 AND t.table_type = 'BASE TABLE'
WHERE c.table_schema = '${DB_NAME}'
GROUP BY c.table_name
ORDER BY c.table_name;
SQL

if ! mysql "${MYSQL_ARGS[@]}" "$DB_NAME" < "$TMP/gen.sql" > "$TMP/cols.tsv" 2> "$TMP/err"; then
  echo "❌ 读 information_schema 失败:" >&2; sed -n '1,10p' "$TMP/err" >&2; exit 1
fi
if [ ! -s "$TMP/cols.tsv" ]; then
  echo "❌ ${DB_NAME} 里一张基表都没有 —— 拒绝把「空库」当成「验收通过」" >&2
  exit 1
fi

# ─── 要检查哪些表 ───
# 给了 bounds 文件：**以 bounds 为准**。要回答的问题是「包里那些表有没有原样落地」，
# 不是「两边表集合是否相同」。目标库多出来的表（历史遗留、后加的）不参与比对，
# 否则一个无关的旧表就能让每一次验收都报红 —— 那正是「习惯了总有一行对不上」的开头。
# 反过来，bounds 里有、目标库没有，是实打实的失败，必须停。
if [ -n "$BOUNDS_FILE" ]; then
  cut -f1 "$BOUNDS_FILE" | while read -r want; do
    [ -z "$want" ] && continue
    awk -F'\t' -v t="$want" '$1==t {found=1} END {exit !found}' "$TMP/cols.tsv" \
      || { echo "❌ bounds 文件里的表 ${want} 在目标库里不存在 —— 验收失败" >&2; exit 1; }
  done || exit 1
  awk -F'\t' 'NR==FNR {want[$1]; next} ($1 in want)' \
      "$BOUNDS_FILE" "$TMP/cols.tsv" > "$TMP/cols.sel.tsv"
  mv "$TMP/cols.sel.tsv" "$TMP/cols.tsv"
fi

# ─── 第 2 趟：拼成一条 UNION ALL，在同一个一致性快照里跑完 ───
{
  echo "START TRANSACTION WITH CONSISTENT SNAPSHOT;"
  first=1
  while IFS=$'\t' read -r tbl cols; do
    [ -z "$tbl" ] && continue
    bound="$(bound_expr "$tbl")"
    if [ "$bound" = "__MISSING__" ]; then
      echo "❌ 内部错误：表 ${tbl} 在 bounds 文件里没有上界" >&2; exit 1
    fi
    if [ "$first" = 1 ]; then first=0; else echo "UNION ALL"; fi
    cat <<SQL
SELECT '${tbl}' AS t, ${bound} AS bound, COUNT(*) AS n,
       IFNULL(BIT_XOR(h),0) AS ckxor, IFNULL(SUM(h),0) AS cksum
FROM (SELECT CAST(CONV(SUBSTRING(MD5(CONCAT_WS(CHAR(1), ${cols})),1,16),16,10) AS UNSIGNED) AS h
      FROM \`${tbl}\` WHERE \`id\` <= ${bound}) x
SQL
  done < "$TMP/cols.tsv"
  echo "ORDER BY t;"
  echo "COMMIT;"
} > "$TMP/check.sql" || exit 1

if ! mysql "${MYSQL_ARGS[@]}" "$DB_NAME" < "$TMP/check.sql" 2> "$TMP/err"; then
  echo "❌ 校验查询失败:" >&2; sed -n '1,10p' "$TMP/err" >&2; exit 1
fi
