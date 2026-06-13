# QuantyTrade 加密备份配置指南

## 架构

```
mysqldump  →  gzip  →  gpg 对称加密  →  rclone  →  对象存储（R2/B2/S3）
                              ↓
                       fail → TG 告警
```

每备份一次都加密上传，加密口令存在你脑子里，**没口令任何人拿到备份也解不开**。

## 一、依赖安装（host 上跑，不在容器里）

```bash
# Debian/Ubuntu
apt update && apt install -y mysql-client gzip gnupg curl

# 装 rclone
curl https://rclone.org/install.sh | sudo bash
```

## 二、选个对象存储

### 推荐：Cloudflare R2（零出口费，恢复时不烧钱）

1. 注册 cloudflare.com
2. 进 R2 → Create Bucket → 命名（如 `quanty-backups`），region 选 `auto`
3. 右上角 **Manage R2 API Tokens** → Create Token
   - Permission: `Admin Read & Write`
   - 复制 **Access Key ID** + **Secret Access Key**
   - 复制 **S3 Endpoint URL**（形如 `https://abc123.r2.cloudflarestorage.com`）

### 备选：Backblaze B2

1. 注册 backblaze.com
2. B2 Cloud Storage → Create Bucket → 命名
3. App Keys → Add a New Application Key
   - 选刚建的 bucket
   - 复制 `keyID` 和 `applicationKey`

## 三、配置 rclone（一次性，5 分钟）

```bash
rclone config

# 按照交互问答：
# n) New remote
# name> r2           ← 名字随便，备份脚本用这个引用
# Storage> s3        ← R2 选 s3，B2 选 b2
# provider> Cloudflare     # R2 选 Cloudflare；B2 选 Other
# env_auth> 1 (Enter the credentials)
# access_key_id> <从 R2/B2 复制>
# secret_access_key> <从 R2/B2 复制>
# region> auto       ← R2 用 auto
# endpoint> <R2 给你的 endpoint，B2 留空>
# location_constraint> 直接回车
# acl> 直接回车
# Edit advanced config?> n
# Keep this "r2" remote?> y
# q) Quit
```

验证连通：
```bash
rclone lsd r2:
# 应该列出你创建的 bucket
```

## 四、配置备份环境变量

```bash
# 用 openssl 生成一个强口令（32 字节 base64）
BACKUP_PASS=$(openssl rand -base64 32)
echo "口令: $BACKUP_PASS"

# ⚠️ 立刻把口令存到密码管理器（1Password / Bitwarden）
# 丢了 = 所有备份都解不开
```

把环境写到 `/etc/quanty-backup.env`：

```bash
sudo tee /etc/quanty-backup.env > /dev/null <<EOF
# rclone 这边的 remote 名 + bucket 名
RCLONE_REMOTE=r2
RCLONE_BUCKET=quanty-backups

# gpg 加密口令（来自上面 openssl 生成的）
BACKUP_PASSPHRASE=$BACKUP_PASS

# 可选：失败时 TG 告警（强烈推荐）
TG_TOKEN=8531784709:AAF6gmy8CAoBzWTPmOfoLWrlG9Eabwa6Sng
TG_CHAT=6938657035
EOF

# 严格权限
sudo chmod 600 /etc/quanty-backup.env
sudo chown root:root /etc/quanty-backup.env
```

## 五、首次测试

```bash
cd /root/work/quanty_trade
sudo bash scripts/db-backup.sh hourly
```

应该看到：

```
🗄  1/4 mysqldump...
   ✅ 12M 原始大小（耗时 8s）
🗜  2/4 gzip...
   ✅ 2.3M gzip 后（耗时 2s）
🔐 3/4 gpg 加密...
   ✅ 2.3M 加密后（耗时 0s）
☁️  4/4 上传到 r2...
   ✅ 上传完成（耗时 4s）
✅ QuantyTrade 备份成功 [hourly]
```

去 R2 web UI 看，应该有：
`quanty-backups/hourly/2026/06/12/quanty_hourly_20260612_193500.sql.gz.gpg`

## 六、装 cron（自动化）

```bash
sudo crontab -e
```

加上：

```
# 每小时 13 分备份（避开整点）
13 * * * * /root/work/quanty_trade/scripts/db-backup.sh hourly >> /var/log/quanty-backup.log 2>&1

# 每日 2:35 全量备份（凌晨低峰）
35 2 * * * /root/work/quanty_trade/scripts/db-backup.sh daily >> /var/log/quanty-backup.log 2>&1

# 每周日 4:35 周备份
35 4 * * 0 /root/work/quanty_trade/scripts/db-backup.sh weekly >> /var/log/quanty-backup.log 2>&1
```

保留策略（脚本里写死了）：
- hourly: **7 天**
- daily: **30 天**
- weekly: **12 周（约 3 个月）**

## 七、恢复（灾难时）

```bash
# 看最近 20 个备份
sudo bash scripts/db-restore.sh

# 用最新 daily
sudo bash scripts/db-restore.sh latest

# 用某个具体备份
sudo bash scripts/db-restore.sh daily/2026/06/12/quanty_daily_20260612_023000.sql.gz.gpg
```

会自动：
1. 先备份当前 DB（safety net，万一恢复失败可回滚）
2. 下载 + 解密 + 恢复

## 八、监控 / 报警

如果你配了 `TG_TOKEN` / `TG_CHAT`：
- ✅ 每次成功备份 → 一条 TG 消息（含大小、耗时）
- ❌ 任何失败（mysqldump / gpg / rclone）→ 立刻 TG 告警

强烈建议**额外加一个 "dead man's switch"**：如果 25 小时没收到 daily 备份成功通知，说明出问题了。

```bash
# 在另一台机器上的 cron
0 8 * * * /usr/bin/curl -s "https://api.telegram.org/bot${TG_TOKEN}/sendMessage" \
  -d "chat_id=${TG_CHAT}" \
  -d "text=⏰ 今天有收到 QuantyTrade daily 备份成功通知吗？没收到的话立刻查"
```

或者用 https://healthchecks.io（免费 tier 够用）：

```bash
# 在 db-backup.sh 成功末尾加一行
curl -s "https://hc-ping.com/<你的 UUID>" > /dev/null
```

healthchecks 那边设 26h 阈值，超期没 ping 自动 TG / email 通知。

## 九、安全清单

- [x] yaml 不在 git 里（`.gitignore` 已加）
- [x] `/etc/quanty-backup.env` 权限 600
- [x] `BACKUP_PASSPHRASE` 已存密码管理器
- [x] rclone 配置文件 `~/.config/rclone/rclone.conf` 权限 600
- [x] R2 / B2 API key 已开启 **只对这一个 bucket 读写**（最小权限）
- [x] 备份成功 TG 通知已联通
- [x] healthchecks.io dead-man 已配（可选）

## 十、估算费用

R2 价格：
- 存储 $0.015/GB·月
- 出口 **$0/GB**（关键！）
- API 请求 ~$4.5/百万次

你 DB 大概 10-50MB，加密压缩后 2-10MB。
- hourly × 24 × 30 = 720 份 / 月 ≈ 7GB
- daily × 30 = 30 份 / 月 ≈ 0.3GB
- weekly × 12 = 12 份 / 月 ≈ 0.12GB
- **总 ~7.5GB ≈ $0.11/月** 几乎免费

## 十一、踩坑提醒

1. **口令丢了 = 全部备份废了**。再写一遍：存密码管理器。
2. **千万别把 `/etc/quanty-backup.env` 加进 git**
3. **每月手动测一次恢复**（在 staging DB 上）。从来没测过的备份等于没备份
4. mysqldump 期间会持锁，DB 比较大时（>1GB）建议改 `--single-transaction --quick`
