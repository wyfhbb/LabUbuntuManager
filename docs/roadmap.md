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

| 批次 | 主题 | 新增命令 | 规模 | 前置依赖 |
|---|---|---|---|---|
| 0 | 地基：统一安装入口 | `install` / `version` / `uninstall` | 小 | — |
| 1 | GPU 管理 | `gpu status` / `gpu top` | 中 | 批次 0 |
| 2 | 磁盘告警与配额 | `disk quota` / 告警链路 | 中 | 批次 0 |
| 3 | Docker 补全 | `docker perm add/del` / `docker mirror` | 小 | — （可随时插队）|
| 4 | 用户生命周期 | `user lock/unlock` / `user key` / 批量创建 | 中 | 批次 0 |
| 5 | 运行时可见性与审计 | `user who` / `top` / 审计日志 | 中 | 批次 0 |
| 6 | 主动告警通道 | `notify` | 中 | 批次 1、2 |

**建议顺序：** 0 → 1 → 2 → 3 → 4 → 5 → 6。
批次 3 与其他批次无耦合，任何时候都可以插队做掉。

---

## 批次 0 — 地基：统一安装入口

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

## 批次 1 — GPU 管理

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

## 批次 2 — 磁盘告警与配额

**为什么做：** [daily-disk-monitor.sh](../cmd/shell/daily-disk-monitor.sh) 每天产出报表就结束了，
没有任何超标处理路径。对比 `user inactive` 已经跑通的 `warn → MOTD → purge` 链路，
磁盘这边是不对称的：用户把盘撑到 100% 不会有任何人收到通知。

**交付内容**

- 磁盘告警链路（复用 inactive 的现成机制，改动最小）：
  - `disk warn --threshold <GB|%>`：把超标用户写入 `motd/warnings.txt`
  - 日常统计脚本跑完后自动触发一次 warn
  - 分区级告警：任一挂载点使用率超警戒线时，MOTD 顶部醒目提示
- `disk quota` 子命令组做硬约束：
  - `disk quota status`：查看当前配额支持情况与各用户软/硬限制
  - `disk quota set <用户> <软限> <硬限>`：ext4 走 `setquota`，XFS 走 project quota
  - `disk quota enable`：检查并引导开启文件系统 quota 支持（挂载参数 + `quotacheck`）
- `motd/warnings.txt` 当前被 inactive 独占（整文件覆写），需要改成分区段写入，
  否则磁盘告警和不活跃告警会互相覆盖 —— 这是做本批次前必须先解决的结构问题

**验收**

- 制造一个超标用户，次日统计后 MOTD 出现磁盘告警，且不影响已有的不活跃告警
- `disk quota set` 后用户写入超过硬限制时被文件系统拒绝
- 不支持 quota 的文件系统上给出明确提示而非静默失败

---

## 批次 3 — Docker 补全（可插队）

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

## 暂不列入

以下缺口已识别但当前不排期，需要时再单独评估：

- **数据备份**：实验室数据量大，备份策略更适合用专门工具（restic / borg）而非塞进本工具
- **cron → systemd timer**：当前三处 cron 工作正常，迁移收益不足以支撑改动成本
- **Web UI / HTTP 接口**：定位是 CLI 工具，加 Web 会显著扩大攻击面
- **`--json` 输出**：等到确实有脚本要消费本工具输出时再做
