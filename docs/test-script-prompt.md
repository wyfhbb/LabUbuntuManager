# 提示词：为 server-mgr 编写端到端测试脚本

本文件是一段**给实现者（人或 AI）的提示词**，用来产出一个覆盖 `server-mgr`
全部功能的端到端测试脚本。功能清单随代码变化，改动命令后请同步更新本文件。

---

## 任务

为 `server-mgr` 写一个 bash 端到端测试脚本，覆盖下面"功能清单"里的**每一条**。
脚本要能一键跑完并输出通过/失败汇总，失败时给出足够定位的上下文。

建议放在 `test/e2e.sh`，配套一个 `test/Dockerfile` 用来起干净的 Ubuntu 环境。
最终要能这样跑：

```bash
make build                 # 产出 GOOS=linux 的二进制
bash test/run-in-docker.sh # 起容器 → 装二进制 → 跑 e2e.sh → 输出汇总
```

---

## ⚠ 硬性约束（先读这一段）

**这个脚本会改系统文件、建删用户、动 `/etc/cron.d` 和 `/etc/apt`，
只能在一次性容器或虚拟机里跑，绝对不能在真实服务器上执行。**

脚本必须在最开头做熔断，条件不满足就直接退出：

- 检测是否在容器/虚拟机内（如 `/.dockerenv` 存在、或要求显式传
  `--i-know-this-destroys-the-machine`），否则拒绝运行并说明原因
- 拒绝在存在真实用户数据的机器上运行（如 `/home` 下已有 UID≥1000 的用户目录时警告并要求确认）

同样地，以下操作有真实破坏力，脚本里要么不碰、要么只在自建的隔离对象上做：

- `user del --purge`：**当前没有二次确认**，执行即删家目录和数据盘目录。
  只能对脚本自己创建的测试用户执行
- `user inactive purge`：有 y/N 确认，但一次删一批。测试时务必用极大的
  `--days` 值确保命中集为空，或只在自建测试用户上验证
- `source set` / `source restore`：改 `/etc/apt/sources.list.d/ubuntu.sources`
- `docker mirror set`：改 `/etc/docker/daemon.json`，并可能重启 Docker（中断运行中的容器）
- `uninstall`：清 `/etc/cron.d/server-mgr-*` 与 `/etc/update-motd.d/99-lab-info`

---

## 已有覆盖 vs. 本脚本要补的

`go test ./...` 已经覆盖了**纯函数**层：`/proc/mounts` 解析、物理盘名推断、
容量格式化、配置读写往返、`nvidia-smi` CSV 解析与故障分类、`/proc/<pid>` 解析、
GPU 按用户聚合、磁盘报表解析与超标筛选、MOTD 告警分文件读写与迁移、APT 镜像识别、
Docker `daemon.json` 合并等。

**本脚本不要重复这些**，专攻 Go 单测够不到的部分：

- 真的把二进制装到 `/usr/local/bin`，验证落盘路径、权限、内容
- 真的写 `/etc/cron.d`，验证 cron 文件格式与内容
- 真的建/删用户，验证家目录、数据盘目录、符号链接
- 真的改 `/etc/bash.bashrc`，验证注入片段可被 bash 解析、且能被干净移除
- 命令的退出码、stderr/stdout 分流、非 root 时的拒绝行为
- `install` → 用 → `uninstall` 的完整生命周期，验证卸载后无残留

---

## 环境准备

测试容器里需要：`bash`、`coreutils`、`findutils`、`passwd`(useradd/userdel/lastlog)、
`cron`、`procps`(ps)。可选：`zsh`（验证 zsh 注入分支）、`docker`（验证 docker 子命令）。

脚本需要自己造出来的夹具：

1. **假数据盘**：`user add` 要求存在"非系统盘"的挂载点。用
   `mount -t tmpfs tmpfs /data` 造一个（容器内需要 `--privileged` 或
   `--cap-add SYS_ADMIN`），否则 `user add` 会因"未找到可用的数据盘挂载点"退出
2. **假 `nvidia-smi`**：放进 `PATH` 前面即可，无需真显卡。至少造三份，
   分别验证正常、故障、无卡三条路径：

   ```sh
   # 正常：两张卡
   case "$1" in
     --query-gpu=*) echo "0, GPU-aaa, 24564, 2100, 22464, 35, 52, 120.35, 450.00, 550.54.14, NVIDIA GeForce RTX 4090" ;;
     --query-compute-apps=*) echo "GPU-aaa, 1, 20480, python" ;;
     -q) echo "CUDA Version                          : 12.4" ;;
   esac

   # 故障：驱动与内核模块版本不一致（最经典的一种）
   echo "Failed to initialize NVML: Driver/library version mismatch" >&2; exit 255

   # 无卡：直接把 nvidia-smi 从 PATH 里拿掉
   ```
3. **假统计报表**：`disk warn` 读 `/var/log/disk-usage/current-usage.txt`。
   直接写一份含超标用户的报表，比等 cron 跑一天快得多。格式为
   `username <TAB> mount <TAB> usage_gb <TAB> full_name`，首行 `# generated: <时间>`
4. **假不活跃用户**：`user inactive` 依赖 `lastlog`、进程、home 目录 mtime 三个信号。
   造一个从未登录、home 目录 mtime 很旧的用户即可

交互式命令要用 stdin 喂答案，清单见下方"需要喂 stdin 的命令"。

---

## 功能清单（逐条覆盖）

格式：`命令` — 权限 — 要断言什么。

### 安装与版本

| 命令 | 权限 | 断言要点 |
|---|---|---|
| `install` | root | `/usr/local/bin/server-mgr` 存在且可执行；`/usr/local/bin/disk-usage` 存在；`/usr/local/lib/server-mgr/config.conf` 生成且含全部 6 个键；重复执行不报错且不覆盖已改过的 config.conf |
| `version` | 任意 | 输出含版本、commit、编译时间、当前路径；当前二进制与已安装的不是同一份时给出提示 |
| `uninstall` | root | 不带 `-y` 时会等待 y/N；`-y` 跳过确认。执行后：二进制、wrapper、`/etc/cron.d/server-mgr-*`、`/etc/update-motd.d/99-lab-info` 全部消失，rc 文件里的注入片段被移除，被禁用的默认 MOTD 脚本恢复可执行位；`/var/log/disk-usage` 与 `/usr/local/lib/server-mgr` **保留** |
| 非 root 执行上述 root 命令 | 普通用户 | 退出码非 0，stderr 提示需要 sudo |

配置文件的 6 个键：`DISK_WARN_PERCENT`、`DISK_USER_WARN_GB`、`DISK_LOG_KEEP_DAYS`、
`DISK_CRON_TIME`、`INACTIVE_CRON_TIME`、`INACTIVE_DAYS`。
额外验证：把 `config.conf` 改坏（写非法值）后各命令仍能工作（回落默认值，不能崩）。

### 磁盘

| 命令 | 权限 | 断言要点 |
|---|---|---|
| `disk` | 任意 | 按物理盘分组输出，过滤掉 loop/squashfs/`/snap/*`；超警戒线的分区带 `[!]` |
| `disk usage` | 任意 | 读报表并按用户汇总；无报表时给出"请先 enable"的指引且退出码非 0 |
| `disk usage --me` | 任意 | 只出当前用户；`sudo` 下要认 `SUDO_USER` 而非 root |
| `disk usage --sort user` / `--sort total` / `--reverse` | 任意 | 排序方向正确 |
| `disk warn` | root | 造超标用户 → 写出 `motd/warnings.d/10-disk.txt`；超标用户消失后再跑一次 → 该文件被删除 |
| `disk warn --gb N` | root | 阈值可覆盖 config.conf |
| `disk monitor enable` | root | 写出 `/usr/local/lib/server-mgr/daily-disk-monitor.sh`、`/etc/cron.d/server-mgr-disk`（含 cron 五段表达式与 `root` 字段）、`disk-usage` wrapper；并立即跑一次产出 `current-usage.txt` |
| `disk monitor run` | root | 重新产出报表，`current-usage.txt` mtime 更新；同时生成当天 `disk-usage-YYYY-MM-DD.log` |
| `disk monitor status` | 任意 | 正确反映启用/未启用、最近统计时间 |
| `disk monitor disable` | root | cron 文件与 wrapper 消失，`/var/log/disk-usage` 数据保留 |
| `disk-usage` wrapper | 普通用户 | 免 sudo 可执行，输出与 `server-mgr disk usage` 一致 |

**统计脚本自身**也要单独测：直接 `bash /usr/local/lib/server-mgr/daily-disk-monitor.sh`，
验证 (a) 只统计 UID≥1000 且可登录的用户，(b) 按 `DISK_LOG_KEEP_DAYS` 清理旧日志，
(c) 跑完会自动触发 `server-mgr disk warn`，(d) 二进制不存在时不报错。

### GPU

| 命令 | 权限 | 断言要点 |
|---|---|---|
| `gpu status`（正常卡） | 任意（**不需要 root**） | 每卡一行，含显存/利用率/温度/功耗；末尾输出驱动版本与 CUDA 版本；`[N/A]` 字段显示成 `N/A` 而不是 0 |
| `gpu top`（有进程） | 任意 | 先"按用户汇总"后"进程明细"；进程能正确归属到用户名（用真实 PID 造测试，root 与普通用户各一个）；读不到属主的进程归到"未知" |
| `gpu top`（无进程） | 任意 | 输出"当前没有进程占用 GPU"，退出码 0 |
| `gpu status` / `gpu top`（驱动故障） | 任意 | 输出含结论、现象、成因、处理步骤四段；退出码非 0。**逐条验证故障分类**（见下） |
| `gpu status` / `gpu top`（无 N 卡） | 任意 | 输出"本机没有 NVIDIA 显卡"，**退出码 0**（正常降级，不是错误） |

故障分类要逐个造 stderr 验证命中正确的诊断：

1. `Failed to initialize NVML: Driver/library version mismatch` → 驱动与内核模块版本不一致，处理步骤含 `sudo reboot` 与 `rmmod`/`modprobe`
2. `NVIDIA-SMI has failed because it couldn't communicate with the NVIDIA driver` → 内核模块未加载，处理步骤含 `lsmod` / `dkms status` / `mokutil --sb-state`
3. `No devices were found` → 疑似掉卡，处理步骤含 `lspci` / `dmesg`
4. `Failed to initialize NVML: Insufficient Permissions` → 权限问题
5. `Failed to initialize NVML: Unknown Error` → NVML 初始化失败（容器场景）
6. 假 `nvidia-smi` 里 `sleep 5` → 超时归类为"GPU 可能已挂起"（验证 2 秒超时确实生效，整条命令不超过约 3 秒）
7. 没有 `nvidia-smi` 但 sysfs 里有 NVIDIA 显卡 → "检测到显卡但没装驱动"，给出 `ubuntu-drivers` 步骤

### MOTD

| 命令 | 权限 | 断言要点 |
|---|---|---|
| `motd set` | root | 写 `/etc/update-motd.d/99-lab-info`（0755）；默认 MOTD 脚本被去掉可执行位并记入 `motd/disabled-scripts.txt`；写出 4 个初始化脚本；向 `/etc/bash.bashrc` 注入 begin/end 成对标记；**没装 zsh 时打印"跳过"而不是报错**；清理遗留的 `/etc/cron.d/server-mgr-motd` 与 `/var/cache/server-mgr/public-ip.txt` |
| `motd show` / `motd render` | 任意 | 依次输出欢迎语、磁盘顶部告警（超线时）、系统信息、局域网 IP、分区明细、GPU 段落、初始化脚本提示、系统提醒、各来源告警 |
| `motd status` | 任意 | 正确反映脚本/二进制/默认脚本/bash 注入/zsh 注入/欢迎语/告警来源七项 |
| `motd reset` | root | 自定义脚本删除、注入移除、默认脚本恢复可执行位；`motd/` 下数据保留 |

**注入片段必须单独重点测**（历史上出过破坏 rc 文件语法的 bug）：

- 注入后 `bash -n /etc/bash.bashrc` 必须通过
- 在片段**紧邻下一行**再放一个 `if [ ... ]; then ... fi` 块，然后 `motd reset`，
  验证相邻块**没有被误删**、且 `bash -n` 仍通过
- 反复 `motd set` 多次，验证片段只有一份、不堆积空行
- 手工造一个老版本单标记片段（只有 `# server-mgr vscode-motd`，无 end 标记），
  验证 `motd set` 能升级成成对标记
- `motd set` 后设 `TERM_PROGRAM=vscode` 起一个 bash 交互 shell，验证会打印 MOTD

**告警分文件机制**（批次 2 的核心改动）：

- `disk warn` 与 `user inactive warn` 各写各的文件，互不覆盖
- 其中一个清空，另一个仍在
- 造一个老版本单文件 `motd/warnings.txt`，验证下次写入时被迁移成
  `warnings.d/20-inactive.txt` 且老文件删除
- 用**普通用户**执行 `motd render`，验证能读到告警且不会因无写权限而报错

### 用户管理

| 命令 | 权限 | 交互 | 断言要点 |
|---|---|---|---|
| `user list` | 任意 | — | 列出用户及数据目录映射 |
| `user add <名>` | root | 全名 + 密码×2 + y/N | 用户建成、家目录存在、每块数据盘上有 `<挂载点>/<用户名>`、家目录里有指向它的符号链接、GECOS 写入了全名；设置的初始密码可直接登录（**弱密码也接受**，如 `123`，且**不再强制首次登录改密**——`chage -l` 不应显示 password must be changed）；用户名含非法字符时拒绝；用户已存在时拒绝；两次密码不一致时只是重新提示、不创建任何东西；确认输入 `n` 时不创建任何东西；**家目录或某块数据盘上已存在同名目录时中止且绝不改动该目录** |
| `user del <名>` | root | — | 用户消失，家目录**保留** |
| `user del <名> --purge` | root | **无确认，直接删** | 家目录与各数据盘目录一并消失 |
| `user passwd <名>` | root | 新密码×2 | 密码修改成功；成功后审计日志新增一条 `动作=user.passwd` |
| `user who` | 任意 | — | 造一个登录会话（或直接喂假 `who` 输出）+ 一个 `.vscode-server` 进程，验证各出现一行：SSH 行含来源 IP 与登录时长、VSCode 行标"(推断)"；无任何会话时输出"当前没有登录会话" |
| `user top` | 任意 | — | 造一个 RSS≥8GiB 且运行≥1天的进程，验证它出现在"长期占用大内存的进程"区；"按用户聚合"区按内存降序、进程数/CPU 合计正确；无大内存长跑进程时该区输出"本次没有符合条件的进程" |
| `user inactive list` | root | — | 列出所有用户及未登录天数 |
| `user inactive warn [--days N]` | root | — | 写 `warnings.d/20-inactive.txt`；无命中时该文件被删除 |
| `user inactive purge --days N` | root | y/N | 输入 `n` 时不删任何用户；**只在自建测试用户上验证删除路径** |
| `user inactive monitor enable [--days N]` | root | — | 写 `/etc/cron.d/server-mgr-inactive`；阈值存进 `config.conf` 的 `INACTIVE_DAYS`；立即执行一次检查 |
| `user inactive monitor disable` | root | — | cron 文件消失，配置保留 |
| `user inactive monitor status` | 任意 | — | 反映定时任务、阈值、告警状态 |

**`user add` 的事务性必须单独重点测**（批次 4 的核心：中途失败不留半成品用户）：

- **回滚干净**：人为制造某块数据盘上 `chown`/`mkdir` 失败（如把假数据盘 `mount -o remount,ro`），
  执行 `user add`，验证失败后 `id <名>` 查不到、`/home/<名>` 不存在、**所有**数据盘上
  都没有 `<挂载点>/<名>` 残留（包括失败发生前已建好的那几块）；stderr 明确说明"已回滚"
- **回滚也失败**：构造回滚阶段无法清理的情形（如让某个已建工作目录变得删不掉），
  验证 stderr **逐条列出**残留的具体路径和手动清理命令（`rm -rf ...` / `userdel -r ...`），
  而不是静默退出
- **不误删已存在目录**：预先在某块数据盘上放一个 `<挂载点>/<名>` 目录（内含一个哨兵文件），
  再 `user add <名>`，验证命令在创建阶段就中止、该目录及哨兵文件**原样保留**、且未创建用户

### 审计

审计日志 `/var/log/server-mgr/audit.log`（0600，仅 root 可读）**只记录成功完成的写操作**。

| 命令 | 权限 | 断言要点 |
|---|---|---|
| `audit` | root | 无记录时输出"暂无审计记录"；有记录时按时间顺序打印，每行 `时间 \| 执行者=… \| 动作=… \| 目标=… \| 详情=…` |
| `audit --user <名>` | root | 只输出执行者或目标含该用户的行 |
| `audit --since <时间>` | root | 只输出该时间之后的行；非法时间格式退出码非 0 |
| 非 root 执行 `audit` | 普通用户 | 退出码非 0，提示需要 sudo（日志 0600 读不到） |

**审计接入必须覆盖每条写操作**（成功后各产生一条记录，动作名固定）：

- `user.add` / `user.del` / `user.del.purge` / `user.passwd` / `user.inactive.purge` /
  `docker.perm.add` / `docker.perm.del` / `docker.mirror.set` / `source.set` /
  `source.restore` / `motd.set` / `motd.reset` / `install` / `uninstall`
- **释放空间要如实记录**：`user del --purge` 与 `user inactive purge` 造一个已知大小的目录，
  验证审计详情里的"释放 X"与实际量级相符（`du -sb` 在删除前测得）
- **只记成功**：制造一次会被回滚的 `user add`（见上文"回滚干净"），验证审计日志里**没有**新增记录
- 日志文件权限必须是 `600`、属主 root；**`uninstall` 后审计日志按设计保留**（同 `/var/log/disk-usage`）

### APT 源

| 命令 | 权限 | 断言要点 |
|---|---|---|
| `source show` | 任意 | 正确识别当前镜像（aliyun/tsinghua/ustc/bfsu/official/未知） |
| `source set <mirror>` | root | 五个镜像名逐个测；写入前备份到 `ubuntu.sources.bak`；非法镜像名给出可选列表并退出码非 0 |
| `source restore` | root | 从备份还原；无备份时明确报错而非静默 |

### Docker

需要容器里有 docker，或至少能容忍它不存在。没有 docker 时这一组应当**优雅跳过而不是失败**。

| 命令 | 权限 | 交互 | 断言要点 |
|---|---|---|---|
| `docker check` | 任意 | — | 正确报告安装状态 |
| `docker install` | root | — | 只在允许联网的环境跑；否则跳过 |
| `docker perm` | 任意 | — | 列出各用户 docker 权限 |
| `docker perm add <名>` | root | y/N | 提示 docker 组等价 root 的风险；确认后用户进入 docker 组；输入 `n` 时不改动 |
| `docker perm del <名>` | root | — | 用户移出 docker 组 |
| `docker mirror set` | root | 重启 y/N | 不带参数用内置默认地址；带参数用命令行地址；写入前备份 `daemon.json.bak`；**`daemon.json` 里已有的其他配置项必须原样保留**（造一个含 `data-root` 等键的文件来验证）；拒绝重启时打印手动命令 |

### 主动告警（notify）

推送要真发到外部，测试时**不要打真实 webhook / SMTP**：用假端点。
起一个本地 HTTP 服务器当假企业微信 webhook（返回 `{"errcode":0,"errmsg":"ok"}`），
或用假 SMTP（如 python `aiosmtpd`/`smtpd`）验证收到邮件。没有条件就 SKIP，不要打真实地址。

| 命令 | 权限 | 交互 | 断言要点 |
|---|---|---|---|
| `notify config` | root | 各字段 + 密码 | 写出 `/usr/local/lib/server-mgr/notify.conf`，**权限必须 0600**；密钥不落进世界可读的 `config.conf`；回车保留旧值、输入 `-` 清空 |
| `notify test` | root | — | 向配置的假端点各发一条；渠道成功打"已发送"、失败打原因并退出码非 0；未配置任何渠道时退出码非 0 |
| `notify check` | root | — | 造一个超线分区（假 `/proc/mounts` 或真 tmpfs 撑满）→ 假 webhook 收到一条；**同一告警再跑一次 check 不应重复发送**（24h 静默窗口，验证去重状态文件 `notify/state` 生效）；未配置渠道时静默跳过、退出码 0 |
| 每日统计脚本触发 | root | — | `bash daily-disk-monitor.sh` 跑完会调用 `notify check`（二进制不在时静默跳过） |

去重与静默：改小 `NOTIFY_SILENCE_HOURS` 或直接改 `notify/state` 里的时间戳，
验证过了窗口后同一告警会再次推送。

### 需要喂 stdin 的命令

写脚本时统一用 here-string / here-doc 喂：

- `user add <名>` → 全名 → 初始密码 → 再次输入密码 → `y`（密码走隐藏输入，
  用管道 / here-doc 喂时 `stty` 会自动降级为明文读取，不影响喂 stdin）
- `user passwd <名>` → 新密码，两次
- `user inactive purge` → `y` 或 `n`
- `docker perm add <名>` → `y` 或 `n`
- `docker mirror set` → 重启确认 `y` 或 `n`
- `notify config` → 企业微信 webhook → SMTP 服务器（留空跳过邮件）→（启用邮件时）端口/账号/密码/发件人/收件人/加密方式 → 静默窗口小时数
- `uninstall`（不带 `-y`）→ `y` 或 `n`

每个带确认的命令都要**正反各测一次**：确认执行、拒绝不执行。

---

## 落盘路径总清单（卸载后残留检查用）

```
/usr/local/bin/server-mgr
/usr/local/bin/disk-usage
/usr/local/lib/server-mgr/config.conf
/usr/local/lib/server-mgr/daily-disk-monitor.sh
/usr/local/lib/server-mgr/motd/header.txt
/usr/local/lib/server-mgr/motd/disabled-scripts.txt
/usr/local/lib/server-mgr/motd/warnings.d/{10-disk,20-inactive}.txt
/usr/local/lib/server-mgr/motd/warnings.txt          # 旧版单文件，应被迁移掉
/usr/local/lib/server-mgr/motd/init/{install,uninstall}-{miniforge,uv}.sh
/usr/local/lib/server-mgr/notify.conf                # 告警推送配置（含密钥，0600），卸载后保留
/usr/local/lib/server-mgr/notify/state               # 告警去重状态（0600），卸载后保留
/etc/cron.d/server-mgr-disk
/etc/cron.d/server-mgr-inactive
/etc/cron.d/server-mgr-motd                          # 废弃，应被清理
/etc/update-motd.d/99-lab-info
/etc/bash.bashrc、/etc/zsh/zshrc                     # VSCode 注入片段
/var/log/disk-usage/{current-usage.txt,disk-usage-*.log,cron.log}
/var/log/server-mgr/audit.log                        # 写操作审计日志，0600，卸载后保留
/var/cache/server-mgr/public-ip.txt                  # 废弃，应被清理
/etc/docker/daemon.json{,.bak}
/etc/apt/sources.list.d/ubuntu.sources{,.bak}
```

`uninstall` 后除 `/var/log/disk-usage`、`/var/log/server-mgr`（审计日志）与
`/usr/local/lib/server-mgr`（**均按设计保留**）之外，其余都不应存在。

---

## 脚本的工程要求

- **可重复运行**：每个用例自带准备与清理，跑两遍结果一致
- **失败不中断**：默认跑完全部用例再汇总，另给 `--fail-fast` 选项
- **输出可读**：每个用例一行 `PASS/FAIL/SKIP + 名称`，失败时附实际输出与期望
- **退出码**：全通过 0，有失败非 0（供 CI 用）
- **SKIP 是一等公民**：没有 docker、没有 zsh、无法 mount tmpfs 时应 SKIP 并说明原因，
  不要伪装成 PASS，也不要算作失败
- **断言要具体**：断言文件内容和退出码，不要只断言"命令没报错"。
  例如别只看 `/etc/cron.d/server-mgr-disk` 存在，要 grep 出 cron 表达式和 `root` 字段
- 用 `LANG=C` 跑，避免 locale 影响 `lastlog` 等外部命令的输出格式

## 明确不要做的

- 不要为了让测试好写而改生产代码。发现不可测的地方，**记下来问**，
  不要顺手加 `--dry-run` 之类的测试专用开关
- 不要重复 `go test` 已覆盖的纯函数逻辑
- 不要在脚本里 `curl` 外网（除非在明确标注为"需要联网"的可跳过用例里）
- 不要触碰宿主机：所有操作都在容器/虚拟机内完成
