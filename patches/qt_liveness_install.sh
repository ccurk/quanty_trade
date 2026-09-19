#!/usr/bin/env bash
# 安装「引擎存活性守望器」的 15 分钟定时任务。
#
# 为什么需要它：2026-09-16 12:24:04 起，策略引擎【空闲时一行日志都不写】，
#   ⇒ 「引擎死了」和「引擎没事干」在日志上完全不可分辨。2026-09-19 一晚因此误报两次。
#   这个脚本装一个【只读】外部判据：不看引擎自己的日志，改看 ①币安K线心跳 ②该实例
#   过去 48h 的日志间隔分布 ③最后一次下单时间，三条独立证据交叉判定死活。
#
# 它【不碰】任何配置、不重启任何东西、不下单。全部动作就是 SELECT + docker logs。
# 卸载：rm /etc/cron.d/qt_liveness /root/qt_liveness_cron.sh /var/log/qt_liveness.log
#
# 前置：/root/qt_liveness.py 已在服务器上（Claude 2026-09-19 已部署并实跑通过）。
# 用法：scp 本文件到服务器后  sudo bash qt_liveness_install.sh
set -euo pipefail

if [ ! -f /root/qt_liveness.py ]; then
  echo "⛔ /root/qt_liveness.py 不存在，先把它传上去" >&2
  exit 1
fi

# ---- 1. 包装器：只留判定相关 3 行；日志超 8000 行自动截半，不无界增长 ----
cat > /root/qt_liveness_cron.sh <<'EOS'
#!/bin/bash
python3 /root/qt_liveness.py 2>&1 | grep -E '^上游证据|^实例|^  判定' >> /var/log/qt_liveness.log
n=$(wc -l < /var/log/qt_liveness.log)
if [ "$n" -gt 8000 ]; then
  tail -4000 /var/log/qt_liveness.log > /tmp/.ql.tmp && mv /tmp/.ql.tmp /var/log/qt_liveness.log
fi
exit 0
EOS
chmod 700 /root/qt_liveness_cron.sh

# ---- 2. 每 15 分钟一次（与 owner 的监控节奏对齐）----
echo "*/15 * * * * root /root/qt_liveness_cron.sh" > /etc/cron.d/qt_liveness
chmod 644 /etc/cron.d/qt_liveness

# ---- 3. 守卫：必须先跑通一次再装 ----
echo "--- 先干跑一次验证 ---"
bash /root/qt_liveness_cron.sh
tail -12 /var/log/qt_liveness.log

echo
echo "✅ 已安装。查看:  tail -40 /var/log/qt_liveness.log"
echo "   cron 生效确认:  ls -l /etc/cron.d/qt_liveness && systemctl is-active cron crond"
echo "   卸载:  rm -f /etc/cron.d/qt_liveness /root/qt_liveness_cron.sh /var/log/qt_liveness.log"
