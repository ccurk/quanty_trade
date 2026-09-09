# RUNBOOK — quanty backend 部署前置条件（台账 #73）

> 2026-09-09 老徐。本文所有"现状"都是当天在 mycloud 上**只读实测**的结果，
> 没跑过的命令不写进来。**全文不含任何凭据的值，只有变量名。**

---

## 0. 一句话结论

**现在执行 `server_deploy_backend.sh` 会直接失败**：它要从
`/etc/quanty/backend.env` 读 6 个凭据，而**服务器上 `/etc/quanty` 这个目录都不存在**。

这是设计上的 fail closed（宁可起不来，也不把凭据写进公开仓库），不是 bug。
但在**所有者把这 6 个值填进去之前，quanty backend 不可部署** —— 包括紧急修复。

在动手之前，先读第 3 节的两个陷阱。**顺序搞错会丢掉一把不可再生的密钥。**

---

## 1. 现状实测（2026-09-09 14:2x，只读）

| 检查项 | 实测结果 |
|---|---|
| `/etc/quanty/backend.env` | **不存在**（`ls: cannot access '/etc/quanty': No such file or directory`，连父目录都没有） |
| `/etc/quanty-backup.env` | 不存在（备份脚本同样起不来，见台账 #68） |
| `/root/work/quanty_trade/.env` | 存在，`-rw------- root root 993`。只有 `MM_GATE_API_KEY` / `MM_GATE_API_SECRET` 两个键，**不覆盖本文说的 6 个** |
| 服务器上的 `server_deploy_backend.sh` | `3698` 字节、Aug 27，**是旧版**，`grep -c etc/quanty` = 0 —— 凭据仍明文内联在里面（台账 #54） |
| 服务器 git HEAD | `326e43e`（本机 main 是 `0433f3a`） |
| 线上容器 | `quanty-backend`，`Up 3 days`，`RestartPolicy=always`，`RestartCount=0`，`StartedAt=2026-09-05T17:56:31Z` |

**线上现在是靠什么活着的**：容器的环境变量是 2026-09-05 用**旧版脚本**（凭据内联）
一次性注入的，之后靠 `--restart always` 一直续命。`docker inspect` 的 `Config.Env`
实测有 31 个键，与本文相关的键名是（**只列键名，值一律不回显 —— 台账 #41 已经在这里栽过一次**）：

```
DB_TYPE DB_HOST DB_PORT DB_NAME DB_USER DB_PASS
REDIS_ENABLED REDIS_ADDR REDIS_DB REDIS_PREFIX REDIS_PASSWORD
EXCHANGE BINANCE_MARKET BINANCE_API_KEY BINANCE_API_SECRET
TELEGRAM_ENABLED TELEGRAM_BOT_TOKEN TELEGRAM_POLL_TIMEOUT_SECONDS
MM_GATE_API_KEY MM_GATE_API_SECRET
MARKETMAKER_CONFIG REBALANCE_CONFIG STRATEGIES_DIR PORT SERVER_PORT GIN_MODE
（其余 GPG_KEY / PATH / LANG / PYTHON_* 来自基础镜像）
```

⚠ **注意这个列表里没有 `JWT_SECRET`，也没有 `CONFIG_ENCRYPTION_KEY`。**
线上这两个值不是从 env 来的，是从容器里挂载的
`/root/work/quanty_trade/conf/conf_pro.yaml` 读的。这就是第 3.1 节那个陷阱的根源。

---

## 2. 脚本在哪一行读它、读哪 6 个

`server_deploy_backend.sh`（本机 main = `0433f3a`；引入者 commit `034cae9`）：

| 行 | 内容 |
|---|---|
| `server_deploy_backend.sh:7` | `QUANTY_ENV_FILE="${QUANTY_ENV_FILE:-/etc/quanty/backend.env}"` |
| `server_deploy_backend.sh:8-18` | 文件存在才 `source`；权限不是 `600` 直接 `exit 1` |
| `server_deploy_backend.sh:63-64` | 必填名单：`DB_PASS REDIS_PASSWORD BINANCE_API_KEY BINANCE_API_SECRET JWT_SECRET CONFIG_ENCRYPTION_KEY` |
| `server_deploy_backend.sh:68-72` | 缺任何一个 → `错误: 以下必填凭据未设置:...` 并 `exit 1` |

**这 6 个就是必填项**（可选项见第 4 节模板下半段）。

同一份文件也被 `server_deploy_docker.sh:9` 读；
MySQL/Redis 容器那两个脚本读的是**另一份** `/etc/quanty/datastore.env`
（`server_deploy_mysql.sh:8`、`server_deploy_redis.sh:7`），本文不覆盖。

---

## 3. 动手前必须知道的两个陷阱

### 3.1 【最重要】`CONFIG_ENCRYPTION_KEY` 只剩服务器上一份，`git pull` 会把它抹掉

- 仓库 main 的 `conf/conf_pro.yaml` 已被清空（第 32-34 行 `jwt_secret: ""` / `config_encryption_key: ""`），
  而**服务器上那份还是有值的旧版**，且 `git status` 显示它 **clean**（= 与服务器 HEAD `326e43e` 一致）。
- 部署仪式里的 `git pull` 会**快进覆盖**这个文件 → 服务器上唯一一份
  `security.config_encryption_key` **当场消失**。
- 这把 key 是 AES-256（`backend/internal/secure/secrets.go:14-33`，实测服务器上该值长度 64 = hex 32 字节），
  用来加密 `quanty_trade.users.configs`（实测 2 行用户）。
  **换一把新的 = 那些用户的交易所配置永久解不开**，脚本注释里写的"切勿更换"就是这个意思。
- `JWT_SECRET` 同理（实测长度 32），只是后果轻得多：换了只是所有人被踢下线重登。

> **所以顺序是：先把值从服务器现有的 `conf_pro.yaml` 抄进 `backend.env`，再 `git pull`。**
> 反过来做，就要靠"从旧镜像/旧容器里刨"来救，而按第 6 节，这里没有第二个版本兜底。

### 3.2 `DB_USER` 必须是 `quanty`，不能填 `root`（脚本会拦）

- `server_deploy_backend.sh:88-92` 硬拦 `DB_USER=root`。
- 实测 `quanty`@`%` 这个账号**已存在**，权限是
  `GRANT ALL PRIVILEGES ON quanty_trade.* `，只对本业务库有权 —— 这一步不需要新建账号。
- 实测线上容器的 `DB_USER` 就是 `quanty`（长度 6，等值比对为真），**但**
  服务器 `conf_pro.yaml` 里的 `db.user` 写的是 `root`。
  **别照抄 yaml 的 `db.user`。**
- 另一个已知事实（台账 #50，本轮复核成立，全程未打印明文）：
  线上容器的 `DB_PASS`、`REDIS_PASSWORD`，以及 `conf_pro.yaml` 里的 `db.pass`、`redis.password`
  **实测两两相等**，都是同一把 8 字符口令，而它同时也是 MySQL **root** 的口令。
  也就是说 `DB_USER=quanty` 这道防线目前**在口令层面是空的**。
  拆成三把口令是所有者的事（台账 #50），本文不处理，但填 env 时请知道这一点。

---

## 4. `/etc/quanty/backend.env` 模板

**下面每个值都留空。填值是所有者的事，任何 agent 不得代填。**

```sh
# /etc/quanty/backend.env   —— 权限必须 600 root:root，被 shell source
# 值含空格或特殊字符时加引号。本文件永远不要进 git、不要 scp 出去。

# ---- 6 个必填（缺任何一个，部署直接 exit 1）----
DB_PASS=                   # ← 服务器 conf_pro.yaml 的 db.pass（注意 3.2：这同时是 root 口令）
REDIS_PASSWORD=            # ← 服务器 conf_pro.yaml 的 redis.password
BINANCE_API_KEY=           # ← 服务器 conf_pro.yaml 的 exchange.binance.api_key
BINANCE_API_SECRET=        # ← 服务器 conf_pro.yaml 的 exchange.binance.api_secret
JWT_SECRET=                # ← 服务器 conf_pro.yaml 的 security.jwt_secret（换了 = 全员重登）
CONFIG_ENCRYPTION_KEY=     # ← 服务器 conf_pro.yaml 的 security.config_encryption_key
                           #   【切勿新生成】见 3.1，换了 = users.configs 永久解不开

# ---- 选填（缺了不拦部署，但对应功能会降级）----
DB_USER=quanty             # 默认就是 quanty；**绝不能填 root**，脚本会拦
TELEGRAM_BOT_TOKEN=        # ← conf_pro.yaml 的 telegram.bot_token；缺了 TG 播报不发
ADMIN_PASSWORD=            # ← conf_pro.yaml 的 admin.password
OPENROUTER_API_KEY=        # ← conf_pro.yaml 的 ai.optimizer.api_key；缺了自动调参不跑
LARK_WEBHOOK_URL=          # ← conf_pro.yaml 的 lark.webhook_url
LARK_SECRET=               # ← conf_pro.yaml 的 lark.secret
```

**这 6 个值现在去哪拿**：服务器上 `/root/work/quanty_trade/conf/conf_pro.yaml`
里全部还在（实测这 6 个字段都非空）。所有者在服务器上直接 `vi` 对照着搬即可，
**不需要去翻密码管理器，也不需要去交易所重新签发**。

> 附带记一笔（不属本文范围，交给台账 #39/#50）：那份 `conf_pro.yaml` 权限是 `644`，
> 即服务器上任何用户可读。别在本轮顺手改它 —— 它正被线上容器挂载着。

### 创建命令（所有者在服务器上执行）

```bash
ssh mycloud
sudo mkdir -p /etc/quanty
sudo install -m 600 -o root -g root /dev/null /etc/quanty/backend.env
sudo vi /etc/quanty/backend.env          # 按上面模板填，别用 echo/cat（会进 shell history）
sudo chmod 600 /etc/quanty/backend.env   # 兜底：脚本会校验，不是 600 直接 exit 1
```

填完立刻跑第 5 节的自检。

---

## 5. 部署前自检：一条命令回答"现在部署会不会 fail"

### 5.1 现在就能跑（不依赖服务器上有没有新脚本）

```bash
ssh mycloud 'f=/etc/quanty/backend.env
[ -f "$f" ] || { echo "FAIL: $f 不存在 → 部署必定失败(fail closed)"; exit 1; }
p=$(stat -c %a "$f"); o=$(stat -c %U:%G "$f")
[ "$p" = 600 ] || { echo "FAIL: 权限 $p != 600"; exit 1; }
[ "$o" = root:root ] || { echo "FAIL: 属主 $o != root:root"; exit 1; }
miss=""
for k in DB_PASS REDIS_PASSWORD BINANCE_API_KEY BINANCE_API_SECRET JWT_SECRET CONFIG_ENCRYPTION_KEY; do
  grep -qE "^[[:space:]]*$k=.+" "$f" || miss="$miss $k"
done
[ -n "$miss" ] && { echo "FAIL: 缺变量:$miss"; exit 1; }
echo "OK: 6 个凭据齐备、权限 600 root:root → 前置条件通过"'
```

它只 `grep` 键名、只 `stat` 元数据，**不打印任何值**。

2026-09-09 实跑输出（当前真实状态）：

```
FAIL: /etc/quanty/backend.env 不存在 → 部署必定失败(fail closed)
```
退出码 `1`。

### 5.2 新脚本上了服务器之后（权威自检）

`server_deploy_backend.sh` 已加 `--check`：跑完**全部**前置校验就停，
不 `docker pull`、不删容器、不起容器。

```bash
ssh mycloud 'cd /root/work/quanty_trade \
  && export BACKEND_VERSION=$(cat .deploy_version_backend) \
  && bash server_deploy_backend.sh --check'
```

`--check` 退出码 0 = 前置条件全过；非 0 = 现在部署会失败，且信息里写明缺什么。
这比 5.1 更权威，因为它跑的就是真部署那条代码路径。

> ⚠ 服务器上现在那份是**旧脚本**（第 1 节），没有 `--check`。
> 得先 `git pull` 才有 —— 而 `git pull` 受 3.1 约束。**所以今天只能用 5.1。**

---

## 6. 部署失败怎么办 —— 先接受一个现实：**没有上一版可以退**

台账 #74 的结论本轮复核成立，并且更精确：

服务器上确实有两个后端镜像 tag，但**它们是同一份东西**：

| tag | image id | created |
|---|---|---|
| `zhaoxianxinclimber108/quanty_trade-backend:20260906015000-d933d91-e82784-backend`（线上在跑） | `sha256:24b1b468…` | `2026-09-05T17:52:23.641057136Z` |
| `quanty_trade-backend:rollback-d933d91` | `sha256:71b156e1…` | `2026-09-05T17:52:23.641057136Z` |

实测 `RootFS.Layers` **完全相同**（两边 md5 都是 `33b25c3b1bcf6ee553caa0c8c135cbf2`），
`Cmd` 都是 `[./main]`，都构建自 commit `d933d91`。

**所以那个叫 `rollback-` 的 tag 不是"上一版"，是同一版的孪生副本。**
它能保的是"registry 拉不动时还能原地重启回 d933d91"，
它保不了的是"新版本上线后发现有问题，退回更早的行为"。**后者当前不存在。**

### 现实可行的三条退路（按代价从低到高）

1. **回到 d933d91（唯一"已知在跑过"的版本）**
   ```bash
   ssh mycloud 'cd /root/work/quanty_trade \
     && export BACKEND_VERSION=20260906015000-d933d91-e82784-backend \
     && bash server_deploy_backend.sh'
   ```
   前提同样是 `backend.env` 已就位 —— **回滚也要过第 5 节那道自检**。
   registry 拉不到时改用本地 tag `quanty_trade-backend:rollback-d933d91`（内容等价，见上表）。

2. **重新构建**。要退到 d933d91 以外的任何 commit，只能现构：`deploy.sh` 是构建脚本。
   这不是分钟级操作，事故当口不要指望它。

3. **停机止血**：`docker stop quanty-backend`（不要 `rm`）。
   做市不报价 = 不再产生新敞口；已有仓位不动。这是唯一秒级见效的动作。

### 三条硬提醒

- **`docker rm -f quanty-backend` 是不可逆的。** 现役容器的 env 是 2026-09-05 用旧脚本
  一次性注入的，容器一删，那份注入现场就没了。删之前请确认 `backend.env` 已填好并通过自检。
- **回滚不会回滚数据库。** 表结构/数据的迁移不在镜像里，退镜像退不掉已写进 MySQL 的东西。
- **`--restart always` 会掩盖崩溃循环。** 部署后别只看 `docker ps` 说 Up，要看
  `docker inspect quanty-backend --format '{{.RestartCount}}'` 和
  `docker logs --tail 100 quanty-backend`。
  启动期凭据不全时是 `panic`（`backend/internal/conf/conf.go:293-306` 的 `MustValidateSecurity`），
  日志里会明写缺哪个。

---

## 7. 红线

- 填 `/etc/quanty/backend.env` 的值、轮换/吊销任何 key —— **所有者的事**，agent 不得代做。
- 任何产出（报告、commit、TG、日志）里**只准出现变量名，不准出现值**。
  排查时给 `docker inspect` 脱敏要用**白名单**（只打你要看的那几个变量），
  不要用 `PASS|SECRET|KEY` 这种黑名单 —— 黑名单漏过 `DATABASE_URL` 是有前科的。

## 相关台账

`#73`（本文主题）、`#36`（待部署的提交）、`#74`（零回滚镜像）、
`#41`（容器 env 明文回显）、`#50`/`#39`（口令复用 + 公开仓库）、`#54`（旧脚本内联凭据）。
