#!/usr/bin/env bash
# quanty-backup.sh — 自动备份 quanty_trade MySQL,由 cron 每天调用。
#
# ⚠️ 这是**实盘**账本。本脚本全程只读:
#   - mysqldump --single-transaction 在 InnoDB 上是"一致性快照读"
#     (START TRANSACTION WITH CONSISTENT SNAPSHOT),不加表锁、不挡线上读写。
#     已实测 quanty_trade 全部 18 张表均为 InnoDB,所以这个前提成立。
#   - 没有 --source-data / --lock-all-tables / --flush-logs —— 那几个才会上全局读锁或写日志。
#   任何人改本脚本前先想清楚:这里多一个写操作,动的就是实盘。
set -euo pipefail
BACKUP_DIR="/var/backups/quanty"
RETENTION_DAYS="14"         # 实测 33MB/份 × 14 = 457MB。实盘账本,历史给足
CONTAINER="quanty-mysql"
DB="quanty_trade"
MIN_BYTES=10000000          # 实测 33MB;低于 10MB 说明 dump 不完整

err() { echo "[$(date '+%F %T')] ERROR: $*" >&2; }

# 容器不在 = 失败(理由同 cardnavi-backup.sh:不做"静默成功")
docker ps --format '{{.Names}}' | grep -q "^${CONTAINER}$" \
  || { err "容器 ${CONTAINER} 未运行 —— 无法备份"; exit 1; }

mkdir -p "${BACKUP_DIR}"
chmod 700 "${BACKUP_DIR}"

TS="$(date +%Y%m%d-%H%M%S)"
OUT="${BACKUP_DIR}/quanty_trade-${TS}.sql.gz"
PART="${OUT}.part"          # 先写 .part,成功才改名(半截文件不能冒充新鲜备份)
ERRF="$(mktemp)"
trap 'rm -f "${ERRF}" "${PART}"' EXIT

# 口令不落到宿主机任何文件:用容器自己 env 里的 MYSQL_ROOT_PASSWORD,
# 且走 MYSQL_PWD 而不是 -p<pass>,免得口令出现在容器内的进程列表里。
set +e
docker exec "${CONTAINER}" sh -c \
  'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysqldump --single-transaction --quick --routines --triggers -uroot '"${DB}" \
  2>"${ERRF}" | gzip > "${PART}"
RC=("${PIPESTATUS[@]}")
set -e

if [ "${RC[0]}" -ne 0 ] || [ "${RC[1]}" -ne 0 ]; then
  err "mysqldump/gzip 失败 (mysqldump=${RC[0]} gzip=${RC[1]}) DB=${DB} 目标=${OUT}"
  err "mysqldump stderr: $(tr '\n' ' ' < "${ERRF}" | head -c 500)"
  exit 1
fi

SIZE="$(stat -c %s "${PART}" 2>/dev/null || echo 0)"
if [ "${SIZE}" -lt "${MIN_BYTES}" ]; then
  err "产出文件过小 (${SIZE} bytes < ${MIN_BYTES}) — 备份视为失败: ${OUT}"
  err "mysqldump stderr: $(tr '\n' ' ' < "${ERRF}" | head -c 500)"
  exit 1
fi

mv "${PART}" "${OUT}"
echo "[$(date '+%F %T')] OK: ${OUT} (${SIZE} bytes)"

find "${BACKUP_DIR}" -name 'quanty_trade-*.sql.gz' -mtime "+${RETENTION_DAYS}" -delete
