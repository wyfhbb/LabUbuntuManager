# 🖥️ server-mgr

> 实验室服务器的"管家"：一条命令回答**谁把盘塞满了、谁占着显卡、谁还在线、谁半年没露面**，
> 顺手把建用户、换源、装 Docker、配登录欢迎语这些重复劳动全收了。

单个静态二进制，`scp` 过去就能跑，无运行时依赖。🐧 面向 Ubuntu 24.04 及更高版本
（`source` 换源依赖 24.04 起启用的 DEB822 源文件，版本代号运行时从 `/etc/os-release`
读取，新版本发布后无需改代码；其余命令对更早的版本也适用）。

📊 **每条命令的真实输入输出**都在这里：[端到端实测报告](docs/e2e-report.md) ——
所有输出都是真跑出来的，不是手写示例。

---

## 😤 它解决什么

实验室服务器的日常大概是这样的：

| 场景 | 以前 | 现在 |
|---|---|---|
| 盘满了 | 挨个 `du -sh` 猜是谁 | 🥇 `disk usage` 直接出排行榜，超标的还会被点名挂到登录页 |
| 抢卡纠纷 | `nvidia-smi` 只有 PID，不知道是谁 | 🎯 `gpu top` 按人聚合："张三 占着 0 号卡 19 GB，跑了 3 天" |
| 新人入职 | `useradd` → 建目录 → 建软链 → 改权限 → 设密码，漏一步就返工 | ✨ `user add zhangsan` 一条龙，中途失败自动回滚，不留半成品 |
| 谁在用机器 | `who` 看不到 VSCode Remote | 👀 `user who` 把 SSH 和 VSCode 会话一起列出来 |
| 有人半年没来 | 没人知道，磁盘就这么占着 | 🧹 `user inactive` 自动发现 + 挂告警 + 确认后清理 |
| 显卡突然用不了 | `Failed to initialize NVML: ...` 然后一脸茫然 | 🩺 `gpu status` 直接告诉你"驱动升级后没重启"，附处理步骤 |
| 半夜盘满了 | 第二天有人来问才知道 | 📣 `notify` 推到企业微信 / 邮箱 |
| 谁删了数据 | 查无对证 | 📜 `audit` 记着每一次写操作：谁、何时、删了多少 |

---

## 🚀 快速开始

```bash
make build            # 开发机上编译（GOOS=linux 静态二进制，注入版本号）
make deploy           # scp 到 .env 里配的 DEPLOY_SERVER
```

到服务器上：

```bash
sudo ./server-mgr install     # 唯一的安装入口：装二进制 + disk-usage 快捷命令 + 默认配置

# 按需开功能
sudo server-mgr disk monitor enable            # 📅 每天 01:00 统计各用户占用 + 超标告警
sudo server-mgr motd set                       # 👋 自定义登录欢迎信息（含 VSCode 终端）
sudo server-mgr user inactive monitor enable   # 🧹 每天 02:00 检查长期不登录用户
sudo server-mgr notify config                  # 📣 企业微信 / 邮件推送
```

不想装也行——`gpu status`、`disk`、`user who`、`user top` 这些只读命令
把二进制拷过去直接跑就有输出，不需要 root，也不留任何文件。

卸载同样一条命令：`sudo server-mgr uninstall`（数据与审计日志按设计保留）。

---

## ✨ 功能一览

> 每个小节末尾的"详细实测"链接直达 [实测报告](docs/e2e-report.md) 对应章节，
> 那里有完整的输入、输出、落盘路径与错误分支。

### 💾 磁盘

**`disk`** —— 按**物理盘**分组列出挂载点，超警戒线的分区自动标 `[!]`：

```
● /dev/sdb  [2.00 TB 物理容量]
  /dev/sdb1  /data       总 2.00 TB  已用 220.00 MB  剩 2.00 TB    0.0%
  /dev/sdb2  /workspace  总 2.00 GB  已用 1.70 GB    剩 308.00 MB  85.0% [!]
```

**`disk usage`** —— 各用户占用排行，**所有用户免 sudo 可用**（快捷命令 `disk-usage`），
支持 `--me` 只看自己、`--sort user` 换排序：

```
  用户名       全名         总计(GB)    明细
* zhangsan  Zhang San  1.91      /home:0.00GB  /data:0.21GB  /mnt/storage:0.00GB  /workspace:1.70GB
```

**`disk monitor enable`** —— 装一个每天 01:00 的统计任务，报表落 `/var/log/disk-usage/`，
跑完自动触发告警与推送。**`disk warn --gb N`** 把超标的人点名写进登录页。

两条告警链路分工明确：用户级（谁占得多，读每日报表）和分区级（盘要满了，实时算、直接顶在 MOTD 最上面）。

📖 [详细实测 →](docs/e2e-report.md#磁盘管理)

### 🎮 GPU

**`gpu status`** —— 每张卡的显存 / 利用率 / 温度 / 功耗，加驱动与 CUDA 版本
（实机实测，2×RTX 4090）：

```
卡号    名称                       显存(已用/总量)           利用率     温度    功耗
0     NVIDIA GeForce RTX 4090  1.00 MB / 47.99 GB  0%      30°C  12 W / 300 W
1     NVIDIA GeForce RTX 4090  1.00 MB / 47.99 GB  0%      31°C  12 W / 300 W

驱动版本:  610.43.02
CUDA 版本: 13.3（驱动支持的最高版本）
```

**`gpu top`** —— 抢卡纠纷的裁判：进程按**用户**聚合，谁占着哪张卡、多少显存、跑了多久，
一目了然（PID 经 `/proc` 映射到用户名，不 fork `ps`）：

```
用户    进程数     显存合计      占用卡号
wyf   2       3.25 GB   0,1
```

**🩺 显卡异常诊断** —— 这是最实用的一块。`nvidia-smi` 报错不再是天书，
七类故障各给出**结论 / 现象 / 成因 / 处理步骤**。比如最经典的"apt 升级驱动后没重启"：

```
⚠ NVIDIA 驱动与已加载的内核模块版本不一致，GPU 当前不可用
  成因: 系统更新了驱动的用户态库，但内核里跑的还是旧版 nvidia 模块…
  处理: 重启机器（最可靠）: sudo reboot
        对比两边版本: cat /proc/driver/nvidia/version …
```

还有：内核模块没加载、掉卡、权限不足、NVML 未知错误、查询超时（2 秒硬超时，
不会让挂起的驱动拖住所有人登录）、有卡没装驱动。异常会在 MOTD 里标红一行，
不会把整屏排障说明糊到登录页。没有 N 卡的机器？整段自动跳过，**不报错**。

📖 [详细实测 →](docs/e2e-report.md#gpu-管理)

### 👥 用户

**`user add zhangsan`** —— 建账号、在**每块数据盘**上开工作目录、在家目录里建软链、
设初始密码，一条龙：

```
  家目录：  /home/zhangsan
  工作目录：/data/zhangsan        符号链接：/home/zhangsan/data -> /data/zhangsan
  工作目录：/workspace/zhangsan   符号链接：/home/zhangsan/workspace -> /workspace/zhangsan
```

⚡ 关键设计：**先收齐并校验全部输入，再动系统**；中途任何一步失败都会**回滚干净**
（实测：把数据盘挂成只读，失败后 `id` 查不到人、所有盘上都没残留、审计里也没记录）；
已存在的同名目录**一律不碰**，在创建前就停下来报错。

**`user del [--purge]`** 删账号（可连数据一起删，释放量写进审计）、
**`user passwd`** 改密码、**`user list`** 看谁有哪些数据目录。

**`user who`** —— 谁在线，SSH 来源 IP + 登录时长，连 VSCode Remote 会话也能推断出来：

```
用户        类型                 来源             时长     会话
zhangsan  SSH                192.168.10.42  3h30m  pts/0
zhangsan  VSCode Remote(推断)  -              0m     pid 578
```

**`user top`** —— 按用户聚合的 CPU / 内存排行，并单独揪出"RSS ≥ 8 GiB 且跑了 ≥ 1 天"的进程。

**`user inactive`** —— 综合 `lastlog`、运行中的进程、家目录 mtime 三个信号判断活跃度：
`list` 看谁多久没来、`warn` 挂到登录页公示、`purge --days 180` 确认后清理、
`monitor enable` 每天自动查。

📖 [详细实测 →](docs/e2e-report.md#用户管理)

### 👋 MOTD 登录欢迎信息

`motd set` 之后，所有人登录（**含 VSCode Remote 终端**）都会看到：

```
欢迎使用 AI 实验室 GPU 服务器 lab-server-01，问题请联系管理员 wyf。   ← ✏️ 这行可改，见下

  ⚠ 磁盘告警: /workspace 使用率 85.0%，仅剩 308.00 MB
              请及时清理，查看各用户占用: disk usage

  lab-server-01 · Ubuntu 24.04.4 LTS · Up 23 小时
  局域网 172.17.0.2

  /data         220.00 MB / 2.00 TB  (0.0%)
  /workspace    1.70 GB / 2.00 GB    (85.0%) [!]

  GPU 0  NVIDIA GeForce RTX 4090  空闲 47.40 GB / 47.99 GB  0%  30°C

  环境初始化脚本：
    bash …/install-miniforge.sh  # 安装 Miniforge3 (conda)
    bash …/install-uv.sh         # 安装 uv (Python 包管理)

⚠ 以下用户磁盘占用超过 1.00 GB
  zhangsan (Zhang San) — 共 1.91 GB  …
```

✏️ **只有第一行欢迎语是可改的**，其余段落全部实时生成。出厂默认是
「欢迎使用实验室服务器！如需帮助请联系管理员。」，上面示例是改过之后的效果。改法：

```bash
sudo vim /usr/local/lib/server-mgr/motd/header.txt   # 存盘即生效，不用重跑任何命令
```

这个文件只在**第一次** `motd set` 时按默认值创建，之后重复 `motd set`、`motd reset`
甚至 `uninstall` 都不会覆盖或删除它——改过的欢迎语一直留着。

⏱️ **登录路径上的实时查询都有超时保护**：分区用量 1 秒、GPU 2 秒。
掉盘后的 SCSI 重试、备份窗口里被 `fsfreeze` 冻住的文件系统都会让 `statfs` 卡进
不可中断的 D 状态——超时到了就跳过那一段并明确提示，**绝不让一个坏挂载点把所有人挡在门外**。
（重活早就不在这条路上了：扫全盘的 `du` 在每天深夜的 cron 里跑，MOTD 只读现成报表。）

`motd status` 一眼看清七个环节是否到位；
`motd reset` 恢复系统默认。注入 shell 的片段带成对标记，删除时按标记区间精确移除——
**不会误伤相邻的 shell 代码**（这有专门的回归测试，因为历史上真出过事 😅）。

📖 [详细实测 →](docs/e2e-report.md#motd-登录欢迎信息)

### 📣 主动告警

别再等用户登录才发现问题。`notify` 扫三个源——**分区使用率超线**、**GPU 异常**、
**需要重启**——推到企业微信机器人或邮箱：

```
【server-mgr 告警】磁盘分区使用率告警：/workspace
挂载点 /workspace 使用率 85%（已用 2/2 GB，设备 /dev/sdb2），已超过警戒线。
主机: lab-server-01
时间: 2026-07-25 22:17:32
```

`notify config` 交互式配置（密钥落 0600 的独立文件，不进世界可读的主配置）、
`notify test` 发测试消息、`notify check` 手动扫一遍。同一告警默认 24 小时内不重复轰炸 🔕。

📖 [详细实测 →](docs/e2e-report.md#主动告警-notify)

### 📜 审计

所有**成功完成**的写操作都记一行，落 `/var/log/server-mgr/audit.log`（0600，普通用户看不到）：

```
2026-07-25T22:15:18+08:00 | 执行者=wyf | 动作=docker.perm.add | 目标=lisi | 详情=加入 docker 组（等价 root 权限）
2026-07-25T22:15:21+08:00 | 执行者=unknown | 动作=user.del.purge | 目标=zhangsan | 详情=释放 1.9GB（家目录 + 3 个数据盘目录）
```

"执行者"取 `SUDO_USER`，所以管理员 `sudo` 执行时记的是**人**（第一行）；
直接以 root 身份跑（cron、`docker exec`）才是 `unknown`（第二行）。
被回滚的操作不记录，删除释放量在删除**之前**量好。`audit --user <名> --since <时间>` 可过滤。

📖 [详细实测 →](docs/e2e-report.md#审计-audit)

### 🐳 Docker

`docker check` 看装没装、`docker install` 用 BFSU 源装、`docker perm` 看谁能免 sudo 用 docker、
`docker perm add/del` 收放权限（授权前会**明确提示 docker 组等价 root**，需二次确认）、
`docker mirror set` 配镜像加速（自动备份，**不破坏 `daemon.json` 里已有的其他配置**）。

📖 [详细实测 →](docs/e2e-report.md#docker-管理)

### 📦 APT 源

`source show` 认当前镜像站、`source set aliyun|tsinghua|ustc|bfsu|official` 换源
（自动备份 + 跑 `apt-get update`）、`source restore` 一键还原。

版本代号从 `/etc/os-release` 读，架构用 `dpkg --print-architecture` 认——
非 x86 机器（arm64 等）自动改用镜像站的 `ubuntu-ports` 路径。`apt-get update`
失败时（镜像站还没同步新版本之类）会自动回滚成换源前的内容，不会留下坏源。

📖 [详细实测 →](docs/e2e-report.md#apt-源管理)

---

## 🧭 命令速查

| 命令 | 权限 | 干什么 |
|---|---|---|
| `install` / `version` / `uninstall` | root（`version` 任意） | 安装、查版本、清理所有系统改动 |
| `disk` | 任意 | 按物理盘分组列出挂载点与容量 |
| `disk usage [-m\|-s\|-r]` | 任意 | 各用户占用排行 |
| `disk warn [--gb N]` | root | 占用超标的用户点名写进 MOTD |
| `disk monitor enable/disable/status/run` | root（`status` 任意） | 每日统计定时任务 |
| `gpu status` / `gpu top` | 任意 | 卡的状态 / 谁占着哪张卡 |
| `user list/add/del/passwd` | root（`list` 任意） | 用户增删改查 + 多盘工作目录 |
| `user who` / `user top` | 任意 | 谁在线 / 谁吃 CPU 内存 |
| `user inactive list/warn/purge/monitor` | root（`monitor status` 任意） | 长期不登录用户 |
| `motd set/show/status/reset` | root（`show`/`status` 任意） | 登录欢迎信息 |
| `notify config/test/check` | root | 企业微信 / 邮件告警 |
| `audit [--user] [--since]` | root | 写操作审计日志 |
| `docker check/install/perm/mirror` | root（`check`/`perm` 任意） | Docker 安装、权限、镜像加速 |
| `source show/set/restore` | root（`show` 任意） | APT 换源 |

`server-mgr --help` 有带选项说明的完整版。

---

## ⚙️ 配置

一个 shell 可 `source` 的 `KEY=VALUE` 文件：`/usr/local/lib/server-mgr/config.conf`
（`install` 时生成，之后**不会被覆盖**，写坏了会静默回落到默认值，不会让登录卡住）。

```ini
DISK_WARN_PERCENT=80      # 分区使用率警戒线
DISK_USER_WARN_GB=500     # 单用户总占用告警线
DISK_LOG_KEEP_DAYS=30     # 报表保留天数
DISK_CRON_TIME="0 1"      # 每日统计时间
INACTIVE_CRON_TIME="0 2"  # 每日不活跃检查时间
INACTIVE_DAYS=180         # 多久没登录算不活跃
```

含密钥的推送配置单独放 `notify.conf`（0600）。
完整落盘路径与卸载后的去留：📖 [落盘路径总表](docs/e2e-report.md#落盘路径总表)。

---

## 🧪 测试

命令会建删用户、改 `/etc/cron.d`、动 MOTD 和 `/etc/apt`，**绝不能在开发机上试跑**。
`test/` 下是一次性的 Docker 演练环境，一条命令跑完全部功能：

```bash
./test/run-e2e.sh            # 编译 → 建镜像 → 跑完所有命令，日志写到 /tmp
./test/run-e2e.sh --shell    # 只布置环境，进容器自己折腾
```

里面用 tmpfs 伪造了三块物理硬盘和五个挂载点（`mount` 的 source 字段会原样进 `/proc/mounts`，
所以多盘识别走的是**真实代码路径**），还有假的 `/sys/block`、`lastlog`、`utmp`、
`nvidia-smi` 和企业微信 webhook。容器 `--rm` 退出即净，开发机零改动 ✅。

```bash
make test                    # go test ./...（93 个用例，覆盖纯函数层）
gofmt -l . && go vet ./...
```

---

## 📁 项目结构

```
cmd/                    各命令实现（disk / gpu / user / motd / notify / audit / docker / source）
cmd/shell/              嵌入二进制的 shell 脚本（每日统计、Miniforge/uv 安装卸载）
test/                   Docker 端到端演练环境
docs/e2e-report.md      📊 端到端实测报告（每条命令的真实输入输出）
docs/disk-interface.md  磁盘查询接口契约
docs/user-interface.md  用户管理接口契约
.env.example            部署目标与内置镜像加速地址模板（复制为 .env，不入库）
```

---

## 📌 已知边界

- GPU 的故障诊断分支与"进程属主读不到"的情况只有桩验证（实机验证要真把驱动弄坏，不值得）
- 邮件渠道与 `docker install` 的联网路径尚未端到端实测
- 告警推送挂在每日统计之后，默认**一天一次**；要分钟级就给 `notify check` 单挂一个短间隔 cron

完整清单见 📖 [已知问题与待补测](docs/e2e-report.md#已知问题与待补测)。
