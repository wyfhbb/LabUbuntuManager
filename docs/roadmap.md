# 待做功能与实施批次

本文档记录 `server-mgr` 已识别的功能缺口，按可独立交付的批次组织。
每个批次自成一个可发布单元：完成后能单独部署到服务器并产生价值，不依赖后续批次。

---

## ⚠ 实施约定（动手前必读）

**遇到任何拿不准的地方，停下来问，不要自作主张。宁可多问一次，也不要多写一行。**

本文档给的是方向和边界，不是详细设计。批次条目里的命令名、参数、输出格式、
落盘路径都是初稿，不构成"已批准的设计"。开工前先确认，不要把初稿当定论直接实现。

**必须先问、不要擅自决定的情况：**

- **需求边界不清** —— 条目只写了"做 X"，但 X 有多种合理做法时，
  列出选项和取舍让我选，不要挑一个就开始写
- **要新增本文档没写的东西** —— 新命令、新 flag、新配置项、新落盘路径、新依赖，
  哪怕觉得"顺手就加了"也先问
- **要动没让你动的代码** —— 顺手重构、改公共函数签名、调整已有命令的输出格式，
  即使确信是改进
- **涉及破坏性或不可逆操作** —— 删用户/删数据/改系统文件（`/etc/passwd`、
  `/etc/bash.bashrc`、`/etc/apt/`、`/etc/cron.d/`）、改文件权限
- **发现本文档的描述与实际代码对不上** —— 说明现状，问怎么办，不要按自己的理解修正
- **实现过程中发现方案行不通** —— 停下来说清楚卡在哪，不要换个方案硬做下去

**不需要问、直接做的情况：** 已明确批准的那一批次内、条目里写死的内容，
以及为它写测试、跑 `gofmt` / `go vet` / `go test`。

**范围纪律：** 一次只做一个批次。批次内发现的其他问题记录到本文档，不要顺手修掉。
"既然都改到这个文件了" 不是扩大范围的理由。

**已知缺陷不等于可以随手改：** 本文档记录的 bug（如批次 0 里的 VSCode 注入三个缺陷）
同样要等对应批次被批准后才动手。

---

## 总览

| 批次 | 主题 | 新增命令 | 规模 | 前置依赖 | 状态 |
|---|---|---|---|---|---|
| 0 | 地基：统一安装入口 | `install` / `version` / `uninstall` | 小 | — | ✅ 已完成 |
| 1 | GPU 管理 | `gpu status` / `gpu top` | 中 | 批次 0 | ✅ 已完成 |
| 2 | 磁盘告警 | `disk warn` / 告警链路 | 中 | 批次 0 | ✅ 已完成 |
| 3 | Docker 补全 | `docker perm add/del` / `docker mirror` | 小 | — （可随时插队）| ✅ 已完成 |
| 4 | 用户生命周期 | `user lock/unlock` / `user key` / 批量创建 | 中 | 批次 0 | 待做 |
| 5 | 运行时可见性与审计 | `user who` / `top` / 审计日志 | 中 | 批次 0 | 待做 |
| 6 | 主动告警通道 | `notify` | 中 | 批次 1、2 | 待做 |

**建议顺序：** 0 → 1 → 2 → 3 → 4 → 5 → 6。
批次 3 与其他批次无耦合，任何时候都可以插队做掉。

---

## 批次 0 — 地基：统一安装入口 ✅ 已完成

**落地时的决策（2026-07-25）**

- 公网 IP 链路选择**删除**：`fetchPublicIP`、`/var/cache/server-mgr/public-ip.txt`、
  每小时抓 IP 的 `/etc/cron.d/server-mgr-motd` 全部移除，`getLocalIPAndGateway`
  改为只返回本机 IP 的 `getLocalIP`；`motd set` / `motd reset` 会顺带清掉老机器上的残留
- `config.conf` 采用 shell 可 source 的 `KEY=VALUE`，`daily-disk-monitor.sh`
  直接 source 同一个文件读保留天数；`install` 时把旧的 `motd/inactive-days.conf`
  迁移进来并删除旧文件
- VSCode 注入片段改为 `# server-mgr vscode-motd begin/end` 成对标记，
  删除按标记区间进行；老的单标记片段在 `motd set` 时自动升级
- `uninstall` 加了 `-y/--yes`（不带则交互确认），保留 `/var/log/disk-usage`
  与 `/usr/local/lib/server-mgr`

**为什么先做：** 后续每个批次都要把二进制装到服务器上。当前
`copyFile(execPath, installedBinPath)` 在三处各写了一遍
（[disk_monitor.go:107](../cmd/disk_monitor.go#L107)、[motd.go:142](../cmd/motd.go#L142)、
[user_inactive.go:277](../cmd/user_inactive.go#L277)），
`/usr/local/bin/server-mgr` 实际是哪次编译取决于最后执行的是哪个命令。
不先收敛这一点，后面每加一个模块就多一处重复。

**交付内容**

- `server-mgr install`：唯一的二进制安装入口，装二进制 + `disk-usage` wrapper，输出已安装版本
- `server-mgr version`：打印版本号、编译时间、git commit（通过 `-ldflags -X` 注入，makefile 同步修改）
- `server-mgr uninstall`：统一清理二进制、wrapper、所有 `/etc/cron.d/server-mgr-*`、`/etc/update-motd.d/99-lab-info`，恢复被禁用的默认 MOTD 脚本；保留 `/var/log/disk-usage` 数据并提示路径
- 三处 `enable`/`set` 命令改为调用统一的 `ensureInstalled()`，不再各自 `copyFile`
- 抽出配置文件 `/usr/local/lib/server-mgr/config.conf`，收敛当前硬编码的阈值：磁盘警戒线 80%（[disk.go:20](../cmd/disk.go#L20)）、日志保留 30 天（[daily-disk-monitor.sh:10](../cmd/shell/daily-disk-monitor.sh#L10)）、cron 时间 01:00/02:00、inactive 阈值 180 天（已有独立配置文件，合并进来）

**顺带修掉**

- MOTD 公网 IP 是死功能：cron 每小时抓 IP 写入 `/var/cache/server-mgr/public-ip.txt`，
  `fetchPublicIP()` 也写，但 [renderNetworkInfo](../cmd/motd.go#L359-L364) 从不读它，
  `getLocalIPAndGateway` 的 `gateway` 返回值同样被丢弃。要么在 MOTD 里显示公网 IP 和网关，要么删掉整条链路
- VSCode MOTD 注入的三个缺陷（功能本身已实现，见 [injectVscodeMotd](../cmd/motd.go#L245)）：
  1. **`removeVscodeMotd` 在窄条件下破坏 rc 文件语法**（已实测复现）：若代码片段之后
     紧邻（中间无空行）另一个以 `if [` 开头的块，该块的首行会被一并删除，
     留下孤立的语句体和多余的 `fi` —— 结果是所有用户的登录 shell 报语法错误。
     根因是 [motd.go:288](../cmd/motd.go#L288) 用行前缀猜测片段边界。
     改为写入 `# server-mgr vscode-motd begin/end` 成对标记，按标记区间删除
  2. **无 zsh 的机器上 `motd set` 报错**（已实测复现）：`/etc/zsh` 目录不存在时
     `os.WriteFile` 失败并打印 "错误: 无法写入 /etc/zsh/zshrc"。
     应先探测 zsh 是否安装，未装则跳过并说明，而非报错
  3. **片段调用 `run-parts /etc/update-motd.d/` 而非直接渲染**：apt 升级
     update-notifier 等包时会恢复默认 MOTD 脚本的可执行位，届时 VSCode 终端里
     会同时冒出 Ubuntu 默认 MOTD。改为直接调用 `server-mgr motd render`，
     既绕开这个问题也更快
- 补充 `motd status`：检查自定义脚本、cron、bash/zsh 注入是否都到位。
  当前无法在不读文件的情况下确认 VSCode 注入还在不在
- `gofmt -l` 当前报 [cmd/disk.go](../cmd/disk.go)、[cmd/motd.go](../cmd/motd.go) 未格式化
- 补纯函数测试：`parseDiskUsageFromMounts`、`physicalDiskName`、`shouldIncludeMount`、
  `formatCapacityByGB`、`detectMirror`、`sortUsers` — 全部零依赖，当前零覆盖

**验收**

- 全新机器上 `sudo server-mgr install` 之后，`disk monitor enable` / `motd set` /
  `user inactive monitor enable` 都不再复制二进制
- `server-mgr version` 输出的 commit 与部署的构建一致
- `sudo server-mgr uninstall` 后 `/etc/cron.d/` 下无残留，默认 MOTD 恢复
- `gofmt -l .` 无输出，`go test ./...` 通过

---

## 批次 1 — GPU 管理 ✅ 已完成

**落地时的决策（2026-07-25）**

- **新增：显卡异常诊断**（本批次外的追加需求，已确认范围为"只分类 `nvidia-smi` 失败"）。
  `classifyGPUFailure` 是纯函数，把 `nvidia-smi` 的原始报错归成
  驱动/内核模块版本不一致、内核模块未加载、认不到卡（掉卡）、权限不足、
  NVML 未知错误、查询超时、未覆盖七类，每类给出「结论 / 现象 / 成因 / 处理步骤」。
  其中最经典的 `Driver/library version mismatch`（apt 升级驱动后没重启）
  明确指向重启或热重载模块，并给出对比两边版本的命令。
  异常同时在 MOTD 里标红一行 + 指向 `gpu status`，不把整屏排障说明塞进登录信息
- **没有 `nvidia-smi` 时区分两种情况**：扫 sysfs 的 `bus/pci/devices/*/vendor`
  找 `0x10de` + class `0x03xx`，有卡 → "该装驱动"并给安装步骤；无卡 → "本机没有
  NVIDIA 显卡"，MOTD 整段跳过、命令正常退出。不依赖 `lspci`（最小化安装未必有 pciutils）
- **不做缓存**：roadmap 原本建议用 `/var/cache/server-mgr/` 做短期缓存降低登录延迟，
  实测健康机器上一次 `--query-gpu` 在百毫秒级，加缓存要引入新落盘路径、TTL、
  以及"MOTD 以 root 跑、VSCode 注入以普通用户跑"带来的读写权限分裂，
  不值当。改为只加 2s 超时，超时本身归类成"GPU 可能已挂起"这一诊断
- **CUDA 版本走 `nvidia-smi -q`**：`--query-gpu` 没有这一项，只能从 `键 : 值`
  输出里取。只在 `gpu status` 调用，不进 MOTD 渲染路径
- `--query-gpu` 的字段顺序把 `name` 放最后、`--query-compute-apps` 把 `process_name`
  放最后，配合 `SplitN` 限制段数，避免显卡名/命令行里的逗号把字段切错
- 进程归属、完整命令行、已运行时长都从 `/proc/<pid>/{status,cmdline,stat}` 读，
  不额外 fork `ps`；`starttime` 从最后一个 `)` 之后数，避开含空格/括号的进程名

**为什么做：** 实验室服务器最稀缺的资源是显卡，当前项目零 GPU 支持。
MOTD 已经渲染了磁盘用量，却不显示 GPU 空闲情况 —— 而后者是用户登录后第一件想知道的事。
"谁占着哪张卡"目前无法回答，抢卡纠纷没有裁判依据。

**交付内容**

- `gpu status`：每张卡的显存占用 / 利用率 / 温度 / 功耗，驱动与 CUDA 版本；
  无 NVIDIA 卡或无 `nvidia-smi` 时给出明确提示而非报错
- `gpu top`：GPU 进程按用户聚合 —— 把 `nvidia-smi --query-compute-apps` 的 PID
  经 `/proc/<pid>/status` 映射到用户名，输出"用户 / 卡号 / 显存 / 已运行时长 / 命令"
- MOTD 增加 GPU 概览段落，与 [renderMotdDisks](../cmd/motd.go#L366) 并列，
  显示每卡空闲显存；无卡的机器自动跳过该段落
- 新增 `cmd/gpu.go`，解析层按 [DiskUsageProvider](../cmd/disk.go#L57) 的模式定义
  `GPUProvider` 接口，`nvidia-smi --format=csv,noheader` 输出的解析函数做成纯函数以便测试

**实现要点**

- 统一走 `nvidia-smi --query-gpu=... --format=csv,noheader,nounits`，不解析人类可读输出
- MOTD 在每次登录时执行，`nvidia-smi` 有几百毫秒延迟：加超时（2s）并考虑复用
  `/var/cache/server-mgr/` 做短期缓存，避免拖慢登录
- 只读命令不要求 root，所有用户可用

**验收**

- 有卡机器上 `gpu top` 能正确把进程归属到用户（含 root 和普通用户）
- 无卡机器上三个入口均正常降级，MOTD 不出现空段落或报错
- CSV 解析函数有单测覆盖，含单卡 / 多卡 / 无进程三种输入

---

## 批次 2 — 磁盘告警 ✅ 已完成

**落地时的决策（2026-07-25）**

- **`disk quota` 子命令组整个放弃**：不做使用量硬限制。原计划的
  `quota status` / `quota set` / `quota enable` 均不实现 ——
  `quota enable` 要改 `/etc/fstab` 挂载参数并跑 `quotacheck`，改坏会导致机器起不来，
  收益不足以支撑这个风险。告警链路已经能回答"谁占了多少"，处置交给管理员
- **告警文件改成 `motd/warnings.d/` 目录**（而非单文件内分段标记）：
  每个来源一个文件，`10-disk.txt` / `20-inactive.txt`，数字前缀即 MOTD 显示顺序。
  各来源各写各的，天然不冲突，也不会因为 `disk warn` 和 `inactive warn`
  同时被 cron 触发而互相覆盖。落盘路径 `motdWarningsDir` /
  `legacyMotdWarningsFile` 改成变量以便测试替换夹具
  - 迁移：写入路径遇到老的 `warnings.txt` 时挪成 `20-inactive.txt` 并删除老文件
    （老文件内容只可能来自 inactive，当时它是唯一写入方）；
    读取路径同时兼容老文件，且**只读不迁移** —— MOTD 渲染可能以普通用户身份
    执行（VSCode 注入的那条），没有写 `/usr/local/lib` 的权限
- **两条告警链路口径分开**：
  - 用户级：`disk warn --gb N`，阈值 `DISK_USER_WARN_GB`（默认 **500 GB**），
    读每日统计报表，超标用户点名写入 `warnings.d/10-disk.txt`
  - 分区级：复用已有的 `DISK_WARN_PERCENT`（默认 80%），**实时计算不落盘**，
    直接在 MOTD 顶部醒目提示。盘一撑满下次登录立刻可见，不必等次日统计
- `daily-disk-monitor.sh` 末尾追加 `server-mgr disk warn`，二进制不在时静默跳过
  （脚本可能是老版本 `enable` 留下的）
- 顺带（为复用而必需，非顺手重构）：`disk usage` 里内联的报表解析抽成
  `parseDiskUsageReport`，`disk warn` 复用；`--me` 的过滤从解析中间挪到解析之后，
  行为不变。`renderMotdDisks` 改为接收已查好的 `[]DiskUsage`，
  避免顶部告警和下面的明细各查一遍 `/proc/mounts`

**为什么做：** [daily-disk-monitor.sh](../cmd/shell/daily-disk-monitor.sh) 每天产出报表就结束了，
没有任何超标处理路径。对比 `user inactive` 已经跑通的 `warn → MOTD → purge` 链路，
磁盘这边是不对称的：用户把盘撑到 100% 不会有任何人收到通知。

**交付内容**

- 磁盘告警链路（复用 inactive 的现成机制，改动最小）：
  - `disk warn --gb <GB>`：把超标用户写入 `motd/warnings.d/10-disk.txt`
  - 日常统计脚本跑完后自动触发一次 warn
  - 分区级告警：任一挂载点使用率超警戒线时，MOTD 顶部醒目提示
- ~~`disk quota` 子命令组做硬约束~~ —— 已放弃，见上方决策
- `motd/warnings.txt` 当前被 inactive 独占（整文件覆写），需要改成分区段写入，
  否则磁盘告警和不活跃告警会互相覆盖 —— 这是做本批次前必须先解决的结构问题

**验收**

- 制造一个超标用户，次日统计后 MOTD 出现磁盘告警，且不影响已有的不活跃告警
  （`TestDiskWarningDoesNotDisturbInactiveWarning` 覆盖）

---

## 批次 3 — Docker 补全（可插队）✅ 已完成

**落地时的决策（2026-07-25）**

- 镜像地址三层取值：代码内置默认 `https://docker.1ms.run` → `make build` 时由
  `.env` 的 `DOCKER_MIRRORS` 经 `-ldflags -X` 覆盖 → `docker mirror set <URL...>`
  命令行显式覆盖。国内镜像仓库大多不可用，目前只内置这一个，后续确认到新地址
  追加进 `.env` 重新构建即可，不必改代码
- `docker mirror set` 写 `daemon.json` 前备份到 `daemon.json.bak`，
  重启 Docker 需交互确认（会中断运行中的容器），拒绝则打印手动命令

**为什么做：** 规模最小、见效最快，且与其他批次零耦合。

**交付内容**

- `docker perm add <用户>` / `docker perm del <用户>`：当前
  [docker.go:198](../cmd/docker.go#L198) 检测到无权限后，只是输出
  "可执行: usermod -aG docker xxx" 让人手敲
- `docker mirror set`：配置 `/etc/docker/daemon.json` 的 registry-mirrors。
  `docker install` 已经用了 BFSU 源，但没配镜像加速，实际拉镜像照样卡死
- 授权时提示 docker 组等价 root 权限的风险，需要二次确认

**验收**

- `docker perm add` 后目标用户免 sudo 可用 docker（需重新登录生效，命令里要说明）
- `docker mirror set` 后 `docker info` 能看到镜像地址，且不破坏 daemon.json 中已有配置

---

## 批次 4 — 用户生命周期

**为什么做：** 当前只有 `add` / `del` 两个极端，缺"停用"这个中间态。
学生毕业时唯一的选择是不可逆删除，风险过高。

**交付内容**

- `user lock <用户>` / `user unlock <用户>`：停用账号但保留全部数据
  （毕业先停用观察一学期再删，比直接 purge 安全得多）；
  `user list` 增加状态列，`user inactive warn` 优先建议 lock 而非 purge
- `user key add/list/del <用户>`：SSH 公钥管理，实验室通常要禁密码登录
- `user add --sudo`：创建时加入 sudo 组
- `user add --batch <csv文件>`：批量创建，开学一次进几十人
- 修复 `user add` 无事务性：[user.go:191-238](../cmd/user.go#L191-L238) 中
  `useradd` 成功后任何一步失败都直接 `os.Exit(1)`，留下没密码、目录不全的半残用户，
  既不回滚也不告诉管理员如何收拾。改为记录已完成步骤，失败时回滚或至少输出清理指令

**验收**

- lock 后用户无法登录，数据完整，unlock 后恢复正常
- 批量创建中途失败不影响已成功的用户，并输出失败清单
- 人为制造 `chown` 失败，验证 `user add` 能回滚干净

---

## 批次 5 — 运行时可见性与审计

**为什么做：** 目前没有任何命令能回答"现在谁登录着、谁在跑什么、谁占了 200G 内存"。
同时 `user del --purge` 和 `inactive purge` 是不可逆的批量删数据操作，当前零记录。

**交付内容**

- `user who`：当前登录会话（含 SSH 来源 IP、登录时长、VSCode Remote 会话）
- `top`（或 `user top`）：按用户聚合的 CPU / 内存占用排行，标出长期占用大内存的进程。
  [detectUserProcesses](../cmd/user_inactive.go#L495) 已经在读 `ps`，但只用于活跃度判断，没有暴露给管理员
- 审计日志 `/var/log/server-mgr/audit.log`：记录所有写操作（谁、何时、执行了什么、影响哪些用户、
  删除了多少数据），`user del`、`inactive purge`、`quota set`、`docker perm` 全部接入
- `server-mgr audit`：查看审计日志，支持按用户 / 时间过滤

**验收**

- 一次 `inactive purge` 后，审计日志能完整还原删了谁、释放多少空间、由谁执行
- 审计日志本身不可被普通用户篡改（权限 600，root only）

---

## 批次 6 — 主动告警通道

**为什么做：** 所有通知目前都靠 MOTD 被动等用户登录。
磁盘 95%、GPU 掉卡这类事故需要主动推送到管理员。

**交付内容**

- `notify config`：配置 webhook（企业微信 / 钉钉）或邮件（SMTP）
- `notify test`：发一条测试消息验证配置
- 接入告警源：磁盘超警戒线、GPU 异常或掉卡、需要重启、apt 安全更新积压
- 告警去重与静默窗口，避免同一问题每天重复轰炸

**验收**

- 制造磁盘超限，管理员在数分钟内收到推送
- 同一告警在静默窗口内不重复发送

---

## 实施中发现并已修掉的问题

- **`physicalDiskName` 会剥掉整盘设备名末尾的数字**（批次 0 补单测时发现，
  2026-07-25 已修）：未分区、整盘直接挂载的数据盘（实验室常见做法，如
  `mkfs.ext4 /dev/nvme0n1` 后挂到 `/data`）曾被算成 `/dev/nvme0n`，
  物理盘标题显示错误盘名且读不到 `/sys/block/*/size`，物理容量为空 ——
  与"按硬盘展示整盘容量"的设计意图相悖。
  改为以 sysfs 为准：`/sys/block/<名字>` 存在即整盘，分区目录下有 `partition`
  文件、其父目录即所属整盘；sysfs 查不到时回落到命名规则
  （`nvme\d+n\d+` / `mmcblk\d+` / `md\d+` / `dm-\d+` 视为整盘名，其余去掉末尾数字）。
  `sysfsRoot` 抽成变量以便用夹具目录做单测

  遗留：`/proc/mounts` 里出现 `/dev/mapper/<vg>-<lv>` 这类 LVM 设备时，
  仍读不到物理容量（显示为无容量的标题行）。需要时再评估是否解析到底层 PV

## 暂不列入

以下缺口已识别但当前不排期，需要时再单独评估：

- **数据备份**：实验室数据量大，备份策略更适合用专门工具（restic / borg）而非塞进本工具
- **cron → systemd timer**：当前三处 cron 工作正常，迁移收益不足以支撑改动成本
- **Web UI / HTTP 接口**：定位是 CLI 工具，加 Web 会显著扩大攻击面
- **`--json` 输出**：等到确实有脚本要消费本工具输出时再做
- **磁盘配额硬限制**（原批次 2 的 `disk quota` 子命令组，2026-07-25 决定放弃）：
  `quota enable` 需要改 `/etc/fstab` 挂载参数、remount、跑 `quotacheck`，
  改坏会导致机器起不来，收益不足以支撑这个风险。
  批次 2 的告警链路已经能回答"谁占了多少"，处置交给管理员
- **GPU 掉卡 / Xid / ECC 主动检测**（批次 1 时评估后未纳入）：
  需要落一份"上次看到几张卡"的状态文件，且 `dmesg` 通常要 root，
  与"只读命令不要求 root"冲突。目前 `nvidia-smi` 报 `No devices were found`
  时已归类为疑似掉卡并给出 `lspci` / `dmesg` 排查命令，够用。
  真要主动推送掉卡告警，更适合放到批次 6 一起做
- **主动比对已安装驱动包版本与已加载内核模块版本**（批次 1 时评估后未纳入）：
  即使 `nvidia-smi` 当前正常，也能提前预警"已升级驱动但未重启"。
  代价是要解析 `dpkg -l` 输出并与 `/proc/driver/nvidia/version` 对齐，
  而真正出问题的那一刻 `nvidia-smi` 一定会报 mismatch、届时已有明确诊断，
  提前量的价值不大
