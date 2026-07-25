# server-mgr 端到端实测报告

`server-mgr` 每一条命令的**实际输入与实际输出**。
功能介绍与快速上手见仓库根目录的 [README](../README.md)，这里只管一件事：**跑给你看**。

每条命令写清三件事：**输入什么**（参数、交互、前置条件、要不要 root）、
**输出什么**（下面所有代码块都是实测原文，不是手写示例）、**改动了磁盘上的什么**。

**采集环境与时间**

| | |
|---|---|
| 采集日期 | 2026-07-25 |
| 主环境 | Docker 一次性容器，Ubuntu 24.04.4 LTS，`--cap-add SYS_ADMIN`、`--rm` |
| 模拟内容 | 3 块物理硬盘 / 5 个挂载点、`/sys/block`、`lastlog`、`utmp`、默认 MOTD 脚本、需重启标记 |
| GPU 部分 | 另在实体服务器上采集：2×RTX 4090（48 GB），驱动 610.43.02，CUDA 13.3 |
| 被测版本 | `adab2ea`（`server-mgr version` 输出中的 commit） |
| 复现方式 | `./test/run-e2e.sh`，详见[测试环境](#测试环境怎么复现) |

容器里跑是**硬性要求**：这些命令会建删用户、改 `/etc/cron.d`、动 MOTD 与
`/etc/bash.bashrc`、改 `/etc/apt` 和 `/etc/docker/daemon.json`，不能在开发机或
真实服务器上试跑。GPU 那几条是纯只读查询，才拿到实体机上采。

---

## 目录

- [命令总览](#命令总览)
- [安装与版本](#安装与版本)
- [磁盘管理](#磁盘管理)
- [GPU 管理](#gpu-管理)
- [用户管理](#用户管理)
- [MOTD 登录欢迎信息](#motd-登录欢迎信息)
- [主动告警 notify](#主动告警-notify)
- [审计 audit](#审计-audit)
- [Docker 管理](#docker-管理)
- [APT 源管理](#apt-源管理)
- [配置文件](#配置文件)
- [落盘路径总表](#落盘路径总表)
- [定时任务](#定时任务)
- [测试环境（怎么复现）](#测试环境怎么复现)
- [已知问题与待补测](#已知问题与待补测)

---

## 命令总览

| 命令 | 权限 | 一句话 |
|---|---|---|
| `install` / `version` / `uninstall` | root（version 任意） | 安装、查版本、清理所有系统改动 |
| `disk` | 任意 | 按物理盘分组列出挂载点与容量 |
| `disk usage` | 任意 | 各用户占用排行（读每日报表） |
| `disk warn` | root | 占用超标的用户点名写进 MOTD |
| `disk monitor enable/disable/status/run` | root（status 任意） | 每日统计定时任务 |
| `gpu status` / `gpu top` | 任意 | 每张卡的状态 / 谁占着哪张卡 |
| `user list/add/del/passwd` | root（list 任意） | 用户增删改查，自动建多盘工作目录 |
| `user who` / `user top` | 任意 | 谁在线 / 谁吃 CPU 内存 |
| `user inactive list/warn/purge/monitor` | root（monitor status 任意） | 长期不登录用户的发现与清理 |
| `motd set/show/status/reset` | root（show/status 任意） | 登录欢迎信息 |
| `notify config/test/check` | root | 企业微信 / 邮件主动告警 |
| `audit` | root | 写操作审计日志 |
| `docker check/install/perm/mirror` | root（check/perm 任意） | Docker 安装、权限、镜像加速 |
| `source show/set/restore` | root（show 任意） | APT 换源 |

完整选项：`server-mgr --help`，或 `server-mgr <命令> --help`。

---

## 安装与版本

### `install` — 唯一的安装入口

**输入**：`sudo server-mgr install`，无参数。**需要 root。**

**输出**（首次安装）：

```
$ /opt/server-mgr install
二进制已安装: /usr/local/bin/server-mgr
快捷命令已安装: /usr/local/bin/disk-usage
默认配置已写入: /usr/local/lib/server-mgr/config.conf

已安装版本: adab2ea-dirty (commit adab2ea, 编译于 2026-07-25 22:01:57)

后续可按需启用各功能：
  sudo server-mgr disk monitor enable            # 每日磁盘统计
  sudo server-mgr motd set                      # 自定义登录欢迎信息
  sudo server-mgr user inactive monitor enable   # 不活跃用户检查
```

重复执行是幂等的，**不会覆盖已经改过的配置**：

```
$ /usr/local/bin/server-mgr install
二进制已是当前运行的版本: /usr/local/bin/server-mgr
快捷命令已安装: /usr/local/bin/disk-usage
配置已存在，保持不变: /usr/local/lib/server-mgr/config.conf
```

**落盘**：`/usr/local/bin/server-mgr`（0755）、`/usr/local/bin/disk-usage`（0755 wrapper）、
`/usr/local/lib/server-mgr/config.conf`（0644）。审计记一条 `install`。

`disk monitor enable`、`motd set`、`user inactive monitor enable` 内部复用同一套安装逻辑，
不会各自复制一份二进制。

### `version`

**输入**：`server-mgr version`，任意用户。

```
$ /opt/server-mgr version
server-mgr adab2ea-dirty
commit:    adab2ea
编译时间:  2026-07-25 22:01:57
当前路径:  /opt/server-mgr
```

版本号 / commit / 编译时间由 `make build` 通过 `-ldflags -X` 注入。
当前运行的二进制与 `/usr/local/bin/server-mgr` 不是同一份时会额外提示，便于确认部署是否生效。

### `uninstall` — 清理所有系统改动

**输入**：`sudo server-mgr uninstall`（交互确认）或加 `-y/--yes` 跳过确认。**需要 root。**

不带 `-y` 时先列清单再等确认：

```
$ /opt/server-mgr uninstall
即将删除：
  二进制:      /usr/local/bin/server-mgr
  快捷命令:    /usr/local/bin/disk-usage
  MOTD 脚本:   /etc/update-motd.d/99-lab-info
  定时任务:    /etc/cron.d/server-mgr-disk, /etc/cron.d/server-mgr-inactive
  shell 注入:  /etc/bash.bashrc、/etc/zsh/zshrc 中的 VSCode MOTD 片段
保留：
  统计数据:    /var/log/disk-usage
  配置与数据:  /usr/local/lib/server-mgr

确认卸载? (y/N): 已取消
```

确认后：

```
$ /opt/server-mgr uninstall -y
...
已删除定时任务: /etc/cron.d/server-mgr-disk
已删除定时任务: /etc/cron.d/server-mgr-inactive
已删除 MOTD 脚本: /etc/update-motd.d/99-lab-info
VSCode MOTD 已移除: /etc/bash.bashrc
VSCode MOTD 已移除: /etc/zsh/zshrc
已恢复 4 个系统默认 MOTD 脚本
已删除快捷命令: /usr/local/bin/disk-usage
已删除二进制: /usr/local/bin/server-mgr

卸载完成。以下内容按设计保留，如需清理请手动删除：
  历史统计数据: /var/log/disk-usage
  配置与欢迎语: /usr/local/lib/server-mgr
```

卸载后 `/etc/cron.d/` 与 `/etc/update-motd.d/` 无残留、默认 MOTD 脚本恢复可执行位、
`/etc/bash.bashrc` 通过 `bash -n` 语法检查。
**按设计保留**：`/var/log/disk-usage`（历史统计）、`/var/log/server-mgr/audit.log`（审计）、
`/usr/local/lib/server-mgr`（配置、欢迎语、告警推送配置）。

---

## 磁盘管理

### `disk` — 多硬盘识别

**输入**：`server-mgr disk`，无参数，任意用户。

**输出**：按**物理盘**分组，盘容量取自 `/sys/block/<盘>/size`，分区容量取自 `statfs`；
使用率超过警戒线（默认 80%，见 `config.conf`）的分区带 `[!]`。
`loop` 设备、`squashfs`、`/snap/*` 一律过滤。

```
$ /usr/local/bin/server-mgr disk
● /dev/sda  [240.00 GB 物理容量]
  /dev/sda1  /boot  总 2.00 GB    已用 0.00 MB  剩 2.00 GB    0.0%
  /dev/sda2  /home  总 236.00 GB  已用 0.04 MB  剩 236.00 GB  0.0%

● /dev/sdb  [2.00 TB 物理容量]
  /dev/sdb1  /data       总 2.00 TB  已用 220.00 MB  剩 2.00 TB    0.0%
  /dev/sdb2  /workspace  总 2.00 GB  已用 1.70 GB    剩 308.00 MB  85.0% [!]

● /dev/sdc  [1.00 TB 物理容量]
  /dev/sdc1  /mnt/storage  总 1.00 TB  已用 0.00 MB  剩 1.00 TB  0.0%
```

分区名到整盘的推断以 sysfs 为准，命名规则兜底：
`sda1 → sda`、`nvme0n1p1 → nvme0n1`、`mmcblk0p1 → mmcblk0`、整盘直挂时保持原样。

### `disk monitor` — 每日用量统计

**输入**：

| 子命令 | 权限 | 说明 |
|---|---|---|
| `disk monitor enable` | root | 装脚本 + 写 cron + 立即跑一次 |
| `disk monitor status` | 任意 | 看是否启用、最近统计时间 |
| `disk monitor run` | root | 立即重新统计一次 |
| `disk monitor disable` | root | 删 cron 与 `disk-usage` 快捷命令，**数据保留** |

```
$ /usr/local/bin/server-mgr disk monitor enable
监控脚本已写入: /usr/local/lib/server-mgr/daily-disk-monitor.sh
定时任务已配置: /etc/cron.d/server-mgr-disk（每天 01:00 执行）
二进制已是当前运行的版本: /usr/local/bin/server-mgr
快捷命令已安装: /usr/local/bin/disk-usage
正在立即执行一次统计，请稍候...

已启用。所有用户可通过以下任意方式查看统计结果：
  disk usage            # 快捷命令（任意用户）
  disk usage --me       # 只看自己
  server-mgr disk usage # 完整命令

$ /usr/local/bin/server-mgr disk monitor status
定时任务:  已启用（每天 01:00 自动执行）
快捷命令:  已安装 /usr/local/bin/disk-usage
日志目录:  /var/log/disk-usage
最近统计:  2026-07-25 22:16:50
```

未启用时 `status` 给出启用方法；`disable` 的输出：

```
$ /usr/local/bin/server-mgr disk monitor disable
定时任务已删除
快捷命令 disk usage 已删除
已禁用。历史统计数据仍保留在 /var/log/disk-usage
```

写出的 cron 文件（`/etc/cron.d` 格式，含 user 字段）：

```
$ cat /etc/cron.d/server-mgr-disk
# server-mgr 磁盘用量每日统计任务
# 由 server-mgr disk monitor enable 自动生成，请勿手动编辑
0 1 * * * root /bin/bash /usr/local/lib/server-mgr/daily-disk-monitor.sh >> /var/log/disk-usage/cron.log 2>&1
```

产出的报表（TAB 分隔，每用户每盘一行）：

```
$ cat /var/log/disk-usage/current-usage.txt
# generated: 2026-07-25 22:23:50
# columns: username	disk	usage_gb	full_name
olduser	/home	0.00	Wang Wu
zhangsan	/home	0.00	Zhang San
zhangsan	/data	0.21	Zhang San
zhangsan	/mnt/storage	0.00	Zhang San
zhangsan	/workspace	1.70	Zhang San
lisi	/home	0.00	Li Si
lisi	/data	0.00	Li Si
lisi	/mnt/storage	0.00	Li Si
lisi	/workspace	0.00	Li Si
```

只统计 UID ≥ 1000 且可登录的用户；家目录用 `du --one-file-system`，
避免顺着符号链接把数据盘的内容重复算进 `/home`。

统计脚本跑完后会自动接着执行 `server-mgr disk warn` 与 `server-mgr notify check`
（二进制不在时静默跳过）。

### `disk usage` — 各用户占用排行

**输入**：`server-mgr disk usage`（或快捷命令 `disk-usage`），**任意用户**，无需 sudo。

| 选项 | 含义 |
|---|---|
| `-m, --me` | 只显示当前用户（`sudo` 下认 `SUDO_USER`，不会显示成 root） |
| `-s, --sort total\|user` | 排序列，默认 `total` 降序 |
| `-r, --reverse` | 反向 |

**前置**：需要先 `disk monitor enable`，否则：

```
$ /usr/local/bin/server-mgr disk usage
错误: 暂无统计数据，请先执行: sudo server-mgr disk monitor enable
[退出码 1]
```

正常输出（`*` 标记当前用户）：

```
$ su - zhangsan -c "disk-usage"
  用户名       全名         总计(GB)    明细
  ------    ----       --------  ----
* zhangsan  Zhang San  1.91      /home:0.00GB  /data:0.21GB  /mnt/storage:0.00GB  /workspace:1.70GB
  olduser   Wang Wu    0.00      /home:0.00GB
  lisi      Li Si      0.00      /home:0.00GB  /data:0.00GB  /mnt/storage:0.00GB  /workspace:0.00GB

数据更新时间: 2026-07-25 22:16:50

$ su - zhangsan -c "disk-usage --me"
  用户名       全名         总计(GB)    明细
  ------    ----       --------  ----
* zhangsan  Zhang San  1.91      /home:0.00GB  /data:0.21GB  /mnt/storage:0.00GB  /workspace:1.70GB
```

### `disk warn` — 占用超标的用户点名写进 MOTD

**输入**：`sudo server-mgr disk warn [--gb N]`。**需要 root。**
阈值默认取 `config.conf` 的 `DISK_USER_WARN_GB`（出厂 500 GB）。
每日统计跑完会自动触发一次，通常不用手动执行。

没人超标时会**清除**已有的磁盘告警：

```
$ /usr/local/bin/server-mgr disk warn
没有用户占用超过 500.00 GB，磁盘告警已清除
```

有人超标时点名写入：

```
$ /usr/local/bin/server-mgr disk warn --gb 1
已将 1 个占用超过 1.00 GB 的用户写入 MOTD
警告文件: /usr/local/lib/server-mgr/motd/warnings.d/10-disk.txt

预览效果: server-mgr motd show

$ cat /usr/local/lib/server-mgr/motd/warnings.d/10-disk.txt
⚠ 以下用户磁盘占用超过 1.00 GB
  zhangsan (Zhang San) — 共 1.91 GB  /home:0.00GB  /data:0.21GB  /mnt/storage:0.00GB  /workspace:1.70GB
  统计时间: 2026-07-25 22:16:50，完整排行: disk usage
```

**两条告警链路是分开的，别混淆**：

- **用户级**（本命令）：读每日报表 → 谁占得多 → 写 `warnings.d/10-disk.txt`，次日统计后更新
- **分区级**：使用率超 `DISK_WARN_PERCENT` → **实时算、不落盘** → 直接顶在 MOTD 最上面

告警按来源分文件存放（`10-disk.txt` / `20-inactive.txt`），互不覆盖；
老版本的单文件 `motd/warnings.txt` 在下次写入时自动迁移成 `warnings.d/20-inactive.txt` 并删除旧文件。

---

## GPU 管理

`gpu status` / `gpu top` **都不需要 root**，所有用户可用。

### 本机无 NVIDIA 显卡（实测）

扫 sysfs 的 PCI 设备发现没有 NVIDIA 显示控制器时，**正常降级、退出码 0**，
MOTD 里整段跳过，不报错：

```
$ /usr/local/bin/server-mgr gpu status
本机没有 NVIDIA 显卡

$ /usr/local/bin/server-mgr gpu top
本机没有 NVIDIA 显卡
```

### 有卡时的输出（实机实测）

以下采集自一台装 2 张 RTX 4090（48 GB 版）、驱动 610.43.02 的实体服务器，
**不需要 root、不需要 install**，把二进制拷过去直接跑即可（全程只读，见下方说明）。

**`gpu status`** —— 每张卡的显存 / 利用率 / 温度 / 功耗 + 驱动与 CUDA 版本：

```
$ ./server-mgr gpu status
卡号    名称                       显存(已用/总量)           利用率     温度    功耗
----  ----                     ---------------     ------  ----  ----
0     NVIDIA GeForce RTX 4090  1.00 MB / 47.99 GB  0%      30°C  12 W / 300 W
1     NVIDIA GeForce RTX 4090  1.00 MB / 47.99 GB  0%      31°C  12 W / 300 W

驱动版本:  610.43.02
CUDA 版本: 13.3（驱动支持的最高版本）

查看谁在占用: server-mgr gpu top
```

**`gpu top`** —— GPU 进程按用户聚合，回答"谁占着哪张卡"。两张卡都空闲时：

```
$ ./server-mgr gpu top
当前没有进程占用 GPU
```

有计算进程时（实测：同一用户在两张卡上各跑一个进程，分别占 2 GB / 512 MB）：

```
$ ./server-mgr gpu top
━━ 按用户汇总 ━━
用户    进程数     显存合计      占用卡号
----  ------  --------  --------
wyf   2       3.25 GB   0,1

━━ 进程明细 ━━
用户    卡号    显存         已运行     PID     命令
----  ----  ----       ------  ---     ----
wyf   0     2.38 GB    1 分     296689  /tmp/gpuhold 0 2048 110
wyf   1     898.00 MB  1 分     296690  /tmp/gpuhold 1 512 110
```

同一时刻的 `gpu status`：

```
$ ./server-mgr gpu status
卡号    名称                       显存(已用/总量)             利用率     温度    功耗
----  ----                     ---------------       ------  ----  ----
0     NVIDIA GeForce RTX 4090  2.39 GB / 47.99 GB    0%      31°C  13 W / 300 W
1     NVIDIA GeForce RTX 4090  908.00 MB / 47.99 GB  0%      31°C  20 W / 300 W
```

（显存合计 3.25 GB 是**进程申请的**显存，`gpu status` 的 2.39 GB + 908 MB 是**卡上实际占用**，
后者含 CUDA context 等开销，两者对不齐是正常的。）

进程归属、完整命令行、已运行时长都从 `/proc/<pid>/{status,cmdline,stat}` 读取，
不额外 fork `ps`；读不到属主（进程刚退出）时归为"未知"、已运行显示"未知"。

**MOTD 中的 GPU 概览段落**（实机实测，无卡的机器整段跳过）：

```
$ ./server-mgr motd show
...
  GPU 0  NVIDIA GeForce RTX 4090  空闲 47.40 GB / 47.99 GB  0%  30°C
  GPU 1  NVIDIA GeForce RTX 4090  空闲 47.40 GB / 47.99 GB  0%  31°C
...
```

**耗时**（MOTD 每次登录都会走这条路径，实测 0.27 秒，远低于 2 秒超时线）：

```
$ time ./server-mgr gpu status >/dev/null
real	0m0.271s
```

**这些命令对实体机的影响：无。** `cmd/gpu.go` 里只有一处 `exec.Command`，就是跑 `nvidia-smi`
（`--query-gpu` / `--query-compute-apps` / `-q` 三条纯查询），不写任何文件、不改驱动状态、
不写审计、也不需要 root；`motd show` 同样只读。唯一的副作用是 NVML 初始化会让空闲卡
短暂唤醒几百毫秒，不影响正在跑的 CUDA 任务。

### 显卡异常诊断

`nvidia-smi` 失败时不会只丢一句 `exit status 255`，而是归类成具体故障并给出
**结论 / 现象 / 成因 / 处理步骤**，退出码 1。以下输出用桩制造对应报错验证——
诊断文本与分类逻辑与真实硬件无关，可直接参考。

> 故障分支**不建议在实体机上验证**：那要把驱动真弄坏（`rmmod nvidia`、`chmod 000 /dev/nvidia*`），
> 有人在用的机器上别这么干。要验证就把 `test/fake-nvidia-smi` 拷过去放在 `PATH` 前面，
> 只影响当前 shell，随时撤掉。

**驱动与内核模块版本不一致**（apt 升级驱动后没重启，最常见）：

```
$ server-mgr gpu status
⚠ NVIDIA 驱动与已加载的内核模块版本不一致，GPU 当前不可用

  现象:
    Failed to initialize NVML: Driver/library version mismatch

  成因:
    系统（多半是 apt 升级）更新了 NVIDIA 驱动的用户态库，但内核里跑的还是旧版 nvidia 模块。
    内核模块只能在重启或卸载后才会换成新版，在此之前所有 CUDA 程序都会失败。

  处理:
    重启机器（最可靠）: sudo reboot
    对比两边版本: cat /proc/driver/nvidia/version   # 已加载的内核模块
                  dpkg -l | grep nvidia-driver      # 已安装的驱动包
    不便重启时可热重载，先确认没人在用 GPU: sudo fuser -v /dev/nvidia*
    再卸载并重新加载模块: sudo rmmod nvidia_uvm nvidia_drm nvidia_modeset nvidia && sudo modprobe nvidia
[退出码 1]
```

其余六类（均已实测命中）：

| 现象 | 结论 | 处理步骤要点 |
|---|---|---|
| `NVIDIA-SMI has failed because it couldn't communicate with the NVIDIA driver` | 内核模块未加载 | `lsmod` / `modprobe` / `dkms status` / `mokutil --sb-state` |
| `No devices were found` | 驱动正常但认不到卡（疑似掉卡） | `lspci \| grep -i nvidia`、`dmesg` 查 PCIe、查供电与插槽 |
| `Failed to initialize NVML: Insufficient Permissions` | 当前用户没有访问 GPU 的权限 | `ls -l /dev/nvidia*`、`sudo nvidia-smi` 对比 |
| `Failed to initialize NVML: Unknown Error` | NVML 初始化失败（容器场景居多） | 先在宿主机跑 `nvidia-smi`，正常则重启容器 |
| `nvidia-smi` 2 秒无响应 | GPU 可能已挂起 | `dmesg \| grep -i xid`、`fuser -v /dev/nvidia*`、重启 |
| 有 NVIDIA PCI 设备但找不到 `nvidia-smi` | 检测到显卡但没装驱动 | `ubuntu-drivers devices` → `autoinstall` → 重启 |

超时保护实测生效（MOTD 每次登录都渲染，不能被挂起的驱动拖住）：

```
$ time SMI_SCENARIO=hang server-mgr gpu status
⚠ nvidia-smi 超过 2s 无响应，GPU 可能已挂起
...
real	0m2.003s
```

MOTD 里只标红一行，不把整屏排障说明塞进登录信息：

```
  GPU ⚠ NVIDIA 驱动与已加载的内核模块版本不一致，GPU 当前不可用
       详情与处理: server-mgr gpu status
```

---

## 用户管理

### `user list`

**输入**：`server-mgr user list`，任意用户。列出 UID ≥ 1000 且可登录的账号，
并检测各数据盘上实际存在的目录。

```
$ /usr/local/bin/server-mgr user list
用户名       UID   全名         主目录             数据目录
ubuntu    1000  Ubuntu     /home/ubuntu    -
olduser   1001  Wang Wu    /home/olduser   -
zhangsan  1002  Zhang San  /home/zhangsan  /data/zhangsan, /mnt/storage/zhangsan, /workspace/zhangsan
lisi      1003  Li Si      /home/lisi      /data/lisi, /mnt/storage/lisi, /workspace/lisi
```

### `user add` — 建用户 + 各数据盘工作目录 + 符号链接

**输入**：`sudo server-mgr user add <用户名>`。**需要 root。**
交互依次输入：**全名 → 初始密码 → 再次输入密码 → y/N 确认**。

- 用户名只允许小写字母、数字、下划线、连字符
- 密码用 `chpasswd` 设置，不过 PAM 强度校验，**弱密码可用**，也不强制首次登录改密
- 数据盘 = `/proc/mounts` 里非系统盘的挂载点（排除 `/`、`/boot`、`/boot/efi`、`/home`
  以及 `/proc`、`/sys`、`/dev`、`/run`、`/tmp`、`/snap` 开头的）

```
$ /usr/local/bin/server-mgr user add zhangsan
请输入用户全名: 请输入初始密码: 请再次输入密码: 即将执行：
  创建用户：zhangsan
  家目录：  /home/zhangsan
  工作目录：/data/zhangsan  (剩余 2.00 TB)
  符号链接：/home/zhangsan/data -> /data/zhangsan
  工作目录：/mnt/storage/zhangsan  (剩余 1.00 TB)
  符号链接：/home/zhangsan/storage -> /mnt/storage/zhangsan
  工作目录：/workspace/zhangsan  (剩余 2.00 GB)
  符号链接：/home/zhangsan/workspace -> /workspace/zhangsan

确认创建? (y/N): 正在创建用户... 完成
正在创建工作目录 /data/zhangsan ... 完成
正在创建符号链接 /home/zhangsan/data ... 完成
正在创建工作目录 /mnt/storage/zhangsan ... 完成
正在创建符号链接 /home/zhangsan/storage ... 完成
正在创建工作目录 /workspace/zhangsan ... 完成
正在创建符号链接 /home/zhangsan/workspace ... 完成
正在设置密码... 完成

用户 zhangsan 创建成功
  家目录：/home/zhangsan
  /home/zhangsan/data -> /data/zhangsan
  /home/zhangsan/storage -> /mnt/storage/zhangsan
  /home/zhangsan/workspace -> /workspace/zhangsan
```

结果：

```
$ ls -l /home/zhangsan/
lrwxrwxrwx 1 zhangsan zhangsan 14 data -> /data/zhangsan
lrwxrwxrwx 1 zhangsan zhangsan 21 storage -> /mnt/storage/zhangsan
lrwxrwxrwx 1 zhangsan zhangsan 19 workspace -> /workspace/zhangsan

$ ls -ld /data/zhangsan /workspace/zhangsan /mnt/storage/zhangsan
drwx------ 2 zhangsan zhangsan 40 /data/zhangsan
drwx------ 2 zhangsan zhangsan 40 /mnt/storage/zhangsan
drwx------ 2 zhangsan zhangsan 40 /workspace/zhangsan

$ grep ^zhangsan: /etc/passwd
zhangsan:x:1002:1002:Zhang San:/home/zhangsan:/bin/bash
```

**先收齐输入再动系统**：全名、密码在动手前就校验完。两次密码不一致只是重新提示，
此时系统还没被碰过；确认处输入 `n` 同样什么都不会创建。

```
$ /usr/local/bin/server-mgr user add sunqi
请输入用户全名: 请输入初始密码: 请再次输入密码:   两次输入不一致，请重新输入
请输入初始密码: 请再次输入密码: 即将执行：
...
确认创建? (y/N): 已取消
```

**中途失败会回滚干净**（实测：把 `/workspace` 挂成只读制造 `mkdir` 失败）：

```
$ /usr/local/bin/server-mgr user add rollbackuser
...
正在创建工作目录 /data/rollbackuser ... 完成
正在创建符号链接 /home/rollbackuser/data ... 完成
正在创建工作目录 /mnt/storage/rollbackuser ... 完成
正在创建符号链接 /home/rollbackuser/storage ... 完成
正在创建工作目录 /workspace/rollbackuser ...
错误: 创建目录 /workspace/rollbackuser 失败: mkdir /workspace/rollbackuser: read-only file system
已回滚：本次创建的内容已全部清理，未残留半成品用户。
[退出码 1]

$ id rollbackuser; ls -ld /home/rollbackuser /data/rollbackuser /mnt/storage/rollbackuser
id: 'rollbackuser': no such user
ls: cannot access '/home/rollbackuser': No such file or directory
ls: cannot access '/data/rollbackuser': No such file or directory
ls: cannot access '/mnt/storage/rollbackuser': No such file or directory

$ /usr/local/bin/server-mgr audit --user rollbackuser
没有符合条件的审计记录          ← 被回滚的操作不写审计
```

回滚只删**本次真正创建**的路径。已存在的同名目录一律不碰，在创建前就中止：

```
$ /usr/local/bin/server-mgr user add ghost
错误: 家目录 /home/ghost 已存在，中止创建（不会改动已存在的目录）
[退出码 1]

$ /usr/local/bin/server-mgr user add ghost2
错误: 工作目录 /data/ghost2 已存在，中止创建（不会改动已存在的目录）
[退出码 1]
```

其他错误路径：

```
$ ... user add zhangsan     → 错误: 用户 'zhangsan' 已存在
$ ... user add ZhangSan     → 错误: 用户名只能包含小写字母、数字、下划线和连字符
```

### `user passwd`

**输入**：`sudo server-mgr user passwd <用户名>`，转交系统 `passwd`，交互输入两次新密码。

```
$ /usr/local/bin/server-mgr user passwd lisi
New password: Retype new password: passwd: password updated successfully
```

### `user del` — 删用户

**输入**：`sudo server-mgr user del <用户名> [--purge]`。**需要 root，且 `--purge` 没有二次确认，执行即删。**

不带 `--purge`：只删账号，家目录与数据盘目录原样保留（属主变成裸 UID）：

```
$ /usr/local/bin/server-mgr user del lisi

$ ls -ld /home/lisi /data/lisi /workspace/lisi /mnt/storage/lisi; id lisi
drwx------ 2 1003 1003  40 /data/lisi
drwxr-x--- 2 1003 1003 160 /home/lisi
drwx------ 2 1003 1003  40 /mnt/storage/lisi
drwx------ 2 1003 1003  40 /workspace/lisi
id: 'lisi': no such user
```

带 `--purge`：家目录与**所有数据盘目录**一并删除，释放量在删除前用 `du -sb` 量好并写进审计：

```
$ /usr/local/bin/server-mgr user del --purge zhangsan
userdel: zhangsan mail spool (/var/mail/zhangsan) not found   ← 测试环境没有 /var/mail，真机不会有这行
正在删除 /data/zhangsan ... 完成
正在删除 /mnt/storage/zhangsan ... 完成
正在删除 /workspace/zhangsan ... 完成

$ /usr/local/bin/server-mgr audit --user zhangsan
2026-07-25T22:14:59+08:00 | 执行者=unknown | 动作=user.add | 目标=zhangsan | 详情=家目录 /home/zhangsan + 3 个数据盘工作目录
2026-07-25T22:15:21+08:00 | 执行者=unknown | 动作=user.del.purge | 目标=zhangsan | 详情=释放 1.9GB（家目录 + 3 个数据盘目录）
```

> 该用户名下还有进程在跑时，`userdel` 可能拒绝删除。先 `pkill -u <用户名>` 再删。

### `user who` — 谁在线

**输入**：`server-mgr user who`，**任意用户**，无需 root。

登录会话解析 `who`（SSH 来源 IP、时长、tty）；VSCode Remote 不是登录会话，
靠扫描各用户的 `.vscode-server` 进程**推断**，取运行最久的进程代表会话起点。

```
$ /usr/local/bin/server-mgr user who
用户        类型                 来源             时长     会话
root      本地                 -              12m    pts/1
zhangsan  SSH                192.168.10.42  3h30m  pts/0
zhangsan  VSCode Remote(推断)  -              0m     pid 578
```

无任何会话时输出 `当前没有登录会话`。

`who` 的时间格式随 locale 变（`2026-07-25 19:49` / C·POSIX 下的 `Jul 25 19:49`），
两种都能解析，cron 等纯 C 环境里调用结果一致：

```
$ LC_ALL=C who
zhangsan pts/0        Jul 25 19:49 (192.168.10.42)
root     pts/1        Jul 25 23:07

$ LC_ALL=C server-mgr user who
用户        类型                 来源             时长     会话
root      本地                 -              12m    pts/1
zhangsan  SSH                192.168.10.42  3h30m  pts/0
zhangsan  VSCode Remote(推断)  -              0m     pid 578
```

### `user top` — 谁吃 CPU / 内存

**输入**：`server-mgr user top`，**任意用户**，无需 root。按用户聚合，内存降序；
并单列出 **RSS ≥ 8 GiB 且运行 ≥ 1 天**的进程。

```
$ /usr/local/bin/server-mgr user top
按用户聚合（按内存占用降序）：
用户        进程数  CPU%  内存(RSS)
root      5    0.6   19.7MB
zhangsan  2    0.0   3.9MB

长期占用大内存的进程（RSS ≥ 8.0GB 且运行 ≥ 1 天）：
  本次没有符合条件的进程
```

### `user inactive` — 长期不登录用户

活跃时间取三个信号里最晚的一个：`lastlog` 最后登录、是否有运行中的进程、
家目录下最近修改时间（从未登录过的用户用创建时间，且不看家目录 mtime——那多半是管理员建的）。

**`user inactive list`**（需要 root）：

```
$ /usr/local/bin/server-mgr user inactive list
用户名       全名         最后登录        未登录天数
------    ----       --------    ----------
ubuntu    Ubuntu     2026-06-10  45 天
olduser   Wang Wu    2025-06-20  400 天
zhangsan  Zhang San  2026-07-25  0 天
lisi      Li Si      2026-07-25  0 天

写入 MOTD 警告:  sudo server-mgr user inactive warn
删除不活跃用户:  sudo server-mgr user inactive purge --days 180
启用定时检查:    sudo server-mgr user inactive monitor enable --days 180
```

**`user inactive warn [--days N]`**（需要 root，默认取 `config.conf` 的 `INACTIVE_DAYS`）：

```
$ /usr/local/bin/server-mgr user inactive warn --days 365
已将 1 个超过 365 天未登录的用户警告写入 MOTD
警告文件: /usr/local/lib/server-mgr/motd/warnings.d/20-inactive.txt

$ cat /usr/local/lib/server-mgr/motd/warnings.d/20-inactive.txt
⚠ 以下用户超过 365 天未登录
  olduser (Wang Wu) — 最后登录: 2025-06-20 (400 天前)
  管理员可执行: sudo server-mgr user inactive purge --days 365
```

无命中时该文件被删除（`没有超过 N 天未登录的用户，不活跃告警已清除`）。

**`user inactive purge --days N`**（需要 root，**不可逆**，有 y/N 确认）：

```
$ /usr/local/bin/server-mgr user inactive purge --days 180
以下 1 个用户超过 180 天未登录，将被删除：

  用户名      全名       最后登录        未登录天数
  ------   ----     --------    ----------
  olduser  Wang Wu  2025-06-20  400 天

⚠ 此操作将删除以上用户及其所有数据，不可恢复！确认删除? (y/N):
正在删除用户 olduser ...
userdel: olduser mail spool (/var/mail/olduser) not found
用户 olduser 已删除
不活跃告警已清除（无不活跃用户）

已完成，共删除 1 个用户
```

输入 `n` 则输出 `已取消`，什么都不删。删除完自动刷新 MOTD 告警，并写审计
（`user.inactive.purge`，含未登录天数与释放空间）。

**`user inactive monitor enable/disable/status`**（enable/disable 需要 root）：

```
$ /usr/local/bin/server-mgr user inactive monitor enable --days 180
阈值已保存: 180 天 → /usr/local/lib/server-mgr/config.conf
二进制已是当前运行的版本: /usr/local/bin/server-mgr
定时任务已配置: /etc/cron.d/server-mgr-inactive（每天 02:00 执行）
正在执行首次检查... 完成（1 个用户已写入 MOTD）

$ cat /etc/cron.d/server-mgr-inactive
# server-mgr 不活跃用户定时检查（每天 02:00）
# 由 server-mgr user inactive monitor enable 自动生成，请勿手动编辑
LANG=C
0 2 * * * root /usr/local/bin/server-mgr user inactive warn 2>/dev/null

$ /usr/local/bin/server-mgr user inactive monitor status
定时任务:  已启用（每天 02:00 自动检查）
阈值天数:  180 天
MOTD 警告:  已写入（2026-07-25 22:15:04）
           /usr/local/lib/server-mgr/motd/warnings.d/20-inactive.txt
```

`--days` 的值会写回 `config.conf` 的 `INACTIVE_DAYS`，之后各命令不带参数时都用它。
`disable` 只删 cron，配置保留。

---

## MOTD 登录欢迎信息

### `motd set`

**输入**：`sudo server-mgr motd set`，无参数。**需要 root。** 幂等，可反复执行。

做四件事：禁用系统默认 MOTD 脚本（去掉可执行位并记录，便于恢复）、装
`/etc/update-motd.d/99-lab-info`、生成 4 个环境初始化脚本、
向 `/etc/bash.bashrc` 与 `/etc/zsh/zshrc` 注入 VSCode Remote 终端的 MOTD 显示。

```
$ /usr/local/bin/server-mgr motd set
默认欢迎语已写入: /usr/local/lib/server-mgr/motd/header.txt
已禁用 4 个系统默认 MOTD 脚本
MOTD 脚本已安装: /etc/update-motd.d/99-lab-info
二进制已是当前运行的版本: /usr/local/bin/server-mgr
Miniforge3 安装脚本已写入: /usr/local/lib/server-mgr/motd/init/install-miniforge.sh
Miniforge3 卸载脚本已写入: /usr/local/lib/server-mgr/motd/init/uninstall-miniforge.sh
uv 安装脚本已写入: /usr/local/lib/server-mgr/motd/init/install-uv.sh
uv 卸载脚本已写入: /usr/local/lib/server-mgr/motd/init/uninstall-uv.sh
VSCode MOTD 已注入: /etc/bash.bashrc
VSCode MOTD 已注入: /etc/zsh/zshrc

MOTD 已启用。用户登录时将看到自定义欢迎信息。
编辑欢迎语: /usr/local/lib/server-mgr/motd/header.txt
预览效果:   server-mgr motd show

环境初始化脚本已生成，用户可执行：
  bash /usr/local/lib/server-mgr/motd/init/install-miniforge.sh
  bash /usr/local/lib/server-mgr/motd/init/install-uv.sh
```

注入的片段带成对标记，删除时按标记区间精确移除：

```
$ tail -6 /etc/bash.bashrc

# server-mgr vscode-motd begin
if [ -n "$VSCODE_IPC_HOOK_CLI" ] || [ "$TERM_PROGRAM" = "vscode" ]; then
	/usr/local/bin/server-mgr motd render 2>/dev/null
fi
# server-mgr vscode-motd end
```

实测（历史上出过破坏 rc 文件语法的 bug，专门回归过）：片段紧邻下一个 `if` 块时，
重复 `motd set` 不会误删相邻块，注入片段始终只有一份，`bash -n /etc/bash.bashrc` 通过。
没装 zsh 的机器打印 `未检测到 zsh，跳过 zsh 注入`，不报错。

### `motd show` / `motd render`

**输入**：`server-mgr motd show`，**任意用户**（`motd render` 是给系统脚本调用的等价隐藏命令）。

段落顺序：欢迎语 → **分区超线告警（顶部醒目）** → 主机信息 → 局域网 IP →
分区明细 → GPU 概览（无卡跳过）→ 环境初始化脚本 → 系统提醒 → 各来源告警。

第一行欢迎语来自 `/usr/local/lib/server-mgr/motd/header.txt`，出厂默认是
「欢迎使用实验室服务器！如需帮助请联系管理员。」，只在**首次** `motd set` 时创建、
之后不再覆盖；下面这段输出里的是改过之后的效果。其余段落全部实时生成。

```
$ /usr/local/bin/server-mgr motd show
欢迎使用 AI 实验室 GPU 服务器 lab-server-01，问题请联系管理员 wyf。

  ⚠ 磁盘告警: /workspace 使用率 85.0%，仅剩 308.00 MB
              请及时清理，查看各用户占用: disk usage

  lab-server-01 · Ubuntu 24.04.4 LTS · Up 23 小时
  局域网 172.17.0.2

  /boot         0.00 MB / 2.00 GB    (0.0%)
  /data         220.00 MB / 2.00 TB  (0.0%)
  /home         0.06 MB / 236.00 GB  (0.0%)
  /mnt/storage  0.00 MB / 1.00 TB    (0.0%)
  /workspace    1.70 GB / 2.00 GB    (85.0%) [!]

  环境初始化脚本：
    bash /usr/local/lib/server-mgr/motd/init/install-miniforge.sh  # 安装 Miniforge3 (conda)
    bash /usr/local/lib/server-mgr/motd/init/install-uv.sh  # 安装 uv (Python 包管理)
    bash /usr/local/lib/server-mgr/motd/init/uninstall-miniforge.sh  # 卸载 Miniforge3
    bash /usr/local/lib/server-mgr/motd/init/uninstall-uv.sh  # 卸载 uv

  12 updates can be applied immediately.
  3 of these updates are standard security updates.
  *** 系统需要重启 ***

⚠ 以下用户磁盘占用超过 1.00 GB
  zhangsan (Zhang San) — 共 1.91 GB  /home:0.00GB  /data:0.21GB  /mnt/storage:0.00GB  /workspace:1.70GB
  统计时间: 2026-07-25 22:16:50，完整排行: disk usage
```

欢迎语直接编辑 `/usr/local/lib/server-mgr/motd/header.txt` 即可，不需要重新 `motd set`。
**普通用户执行也能读到告警**，不会因为没有写 `/usr/local/lib` 的权限而报错（实测）。

#### 登录路径上的超时保护

MOTD 在**每次登录**时渲染（`/etc/update-motd.d/99-lab-info` 与 VSCode 终端注入那条都会跑），
所以这条路径上任何一步卡住 = 所有人登不进来。两处实时查询因此都设了超时：

| 查询 | 超时 | 超时后的表现 |
|---|---|---|
| 分区用量（`statfs` 每个挂载点） | 1 秒 | 跳过磁盘段落，打印 `⚠ 分区用量查询超过 1s 无响应，本次跳过` + 排查提示 |
| GPU（`nvidia-smi`） | 2 秒 | 归类为"GPU 可能已挂起"，MOTD 里只标红一行 |

`statfs` 会卡住的典型场景：掉盘后的 SCSI 重试、被 `fsfreeze` 冻结的文件系统、
失联的网络挂载。这类调用停在不可中断的 D 状态里，syscall 本身取消不掉
（context 也杀不动），所以做法是**不再等它**：查询丢进 goroutine，超时就放弃磁盘段落
继续渲染完。放弃的 goroutine 随进程退出消失（MOTD 是一次性短进程）。

真正的重活不在这条路径上：扫全盘的 `du` 只在每日 cron 的统计脚本里跑，
MOTD 读的是它产出的现成报表。

> 超时分支由单测 `TestListDiskUsageWithTimeout` 覆盖（含"超时后立即返回"的断言）；
> 容器演练里没有真实制造过卡死的挂载点，本报告中所有 MOTD 输出都走的是正常路径。
> 用户显式敲的 `disk` / `disk usage` **不设超时**——那只拖住他自己，
> 且这种时候更该看到命令卡在哪，而不是被悄悄跳过。

### `motd status`

**输入**：`server-mgr motd status`，任意用户。七项逐一体检：

```
$ /usr/local/bin/server-mgr motd status
自定义脚本:  已安装 /etc/update-motd.d/99-lab-info
二进制:      已安装 /usr/local/bin/server-mgr
默认脚本:    已禁用 4 个（记录: /usr/local/lib/server-mgr/motd/disabled-scripts.txt）
bash 注入:   已注入 /etc/bash.bashrc
zsh 注入:    已注入 /etc/zsh/zshrc
欢迎语:      /usr/local/lib/server-mgr/motd/header.txt
MOTD 警告:   磁盘占用超标（10-disk）、长期未登录用户（20-inactive）
             目录: /usr/local/lib/server-mgr/motd/warnings.d
```

### `motd reset`

**输入**：`sudo server-mgr motd reset`。**需要 root。**

```
$ /usr/local/bin/server-mgr motd reset
VSCode MOTD 已移除: /etc/bash.bashrc
VSCode MOTD 已移除: /etc/zsh/zshrc
已恢复 4 个系统默认 MOTD 脚本
已恢复系统默认 MOTD
自定义数据保留在 /usr/local/lib/server-mgr/motd，如需清理请手动删除
```

---

## 主动告警 notify

三个告警源：**分区使用率超警戒线**、**GPU 异常**（本机没卡不算异常，不推）、
**需要重启**（`/var/run/reboot-required`）。
渠道：企业微信机器人 Webhook、SMTP 邮件（starttls / ssl / none）。

### `notify config`

**输入**：`sudo server-mgr notify config`，交互式。**需要 root。**
回车保留当前值，输入 `-` 清空某项；SMTP 服务器留空则整个邮件渠道不启用；
密码走隐藏输入，不会出现在命令行参数里。

```
$ /usr/local/bin/server-mgr notify config
── 告警推送配置 ──
企业微信机器人 Webhook [空]（回车保留，输入 - 清空）:
SMTP 服务器（留空=不启用邮件） [空]（回车保留，输入 - 清空）:
静默窗口（小时，同一告警窗口内不重复推送） [24]（回车保留）:
已保存: /usr/local/lib/server-mgr/notify.conf（权限 0600）
可用 sudo server-mgr notify test 发一条测试消息验证。
```

配置落在与世界可读的 `config.conf` **分开**的 0600 文件里：

```
$ ls -l /usr/local/lib/server-mgr/notify.conf
-rw------- 1 root root 338 /usr/local/lib/server-mgr/notify.conf

$ cat /usr/local/lib/server-mgr/notify.conf
# server-mgr 告警推送配置（含密钥，权限 0600，勿提交到版本库）
# 由 server-mgr notify config 生成
NOTIFY_WECHAT_WEBHOOK=http://127.0.0.1:18080/webhook
NOTIFY_SMTP_HOST=
NOTIFY_SMTP_PORT=587
NOTIFY_SMTP_USER=
NOTIFY_SMTP_PASSWORD=
NOTIFY_SMTP_FROM=
NOTIFY_SMTP_TO=
NOTIFY_SMTP_TLS=starttls
NOTIFY_SILENCE_HOURS=24
```

### `notify test`

**输入**：`sudo server-mgr notify test`。向每个已配置渠道各发一条测试消息。

```
$ /usr/local/bin/server-mgr notify test
企业微信... 已发送
```

对端收到的内容（实测，测试用的本地假 webhook 记录）：

```
【server-mgr 测试】这是一条测试告警，用于验证推送配置。
主机: lab-server-01
时间: 2026-07-25 22:17:32
```

未配置任何渠道时：`错误: 未配置任何推送渠道，先运行 sudo server-mgr notify config`，退出码 1。

### `notify check`

**输入**：`sudo server-mgr notify check`。扫三个源 → 去重 → 推送。
每日磁盘统计跑完会自动触发一次，也可手动执行。

```
$ /usr/local/bin/server-mgr notify check          # 未配置渠道
未配置任何推送渠道，跳过（先运行 sudo server-mgr notify config）

$ /usr/local/bin/server-mgr notify check          # /workspace 超线 + 需要重启
检查完成：本次推送 2 条告警

$ /usr/local/bin/server-mgr notify check          # 静默窗口内不重复推
检查完成：本次推送 0 条告警
```

推送内容：

```
【server-mgr 告警】磁盘分区使用率告警：/workspace
挂载点 /workspace 使用率 85%（已用 2/2 GB，设备 /dev/sdb2），已超过警戒线。
主机: lab-server-01
时间: 2026-07-25 22:17:32

【server-mgr 告警】服务器需要重启
/var/run/reboot-required 存在，内核或核心库已更新，建议择机重启使其生效。
主机: lab-server-01
时间: 2026-07-25 22:17:32
```

去重状态（**推送成功才记入**，失败下次重试；超窗口的条目自动清理）：

```
$ cat /usr/local/lib/server-mgr/notify/state
disk.partition:/workspace	1784988580
reboot.required	1784988580
```

实测把时间戳改成 25 小时前（超出默认 24h 静默窗口）后，同样的告警会再次推送。

> **推送节奏**：`notify check` 挂在每日磁盘统计（默认 01:00）之后，所以默认**一天一次**。
> 需要分钟级就把 `notify check` 单独挂一个短间隔 cron，代码不用改。

---

## 审计 audit

所有**成功完成**的写操作都会记一行到 `/var/log/server-mgr/audit.log`
（目录 0700 / 文件 0600，普通用户读不到）。被回滚的操作不记录。

覆盖的动作名：`user.add`、`user.del`、`user.del.purge`、`user.passwd`、
`user.inactive.purge`、`docker.perm.add`、`docker.perm.del`、`docker.mirror.set`、
`source.set`、`source.restore`、`motd.set`、`motd.reset`、`install`、`uninstall`。

**输入**：`sudo server-mgr audit [--user <名>] [--since <时间>]`。**需要 root。**

```
$ /usr/local/bin/server-mgr audit
2026-07-25T22:14:59+08:00 | 执行者=unknown | 动作=install | 目标=- | 详情=version=adab2ea-dirty commit=adab2ea
2026-07-25T22:14:59+08:00 | 执行者=unknown | 动作=user.add | 目标=zhangsan | 详情=家目录 /home/zhangsan + 3 个数据盘工作目录
2026-07-25T22:14:59+08:00 | 执行者=unknown | 动作=user.passwd | 目标=lisi
2026-07-25T22:15:00+08:00 | 执行者=unknown | 动作=motd.set | 目标=- | 详情=启用自定义 MOTD 并注入 VSCode 终端显示
2026-07-25T22:15:04+08:00 | 执行者=unknown | 动作=user.inactive.purge | 目标=olduser | 详情=未登录 400 天，释放 4.7KB（家目录 + 0 个数据盘目录）
2026-07-25T22:15:04+08:00 | 执行者=unknown | 动作=docker.mirror.set | 目标=- | 详情=registry-mirrors=https://docker.1ms.run
2026-07-25T22:15:04+08:00 | 执行者=unknown | 动作=source.set | 目标=清华大学 | 详情=codename=noble
2026-07-25T22:15:18+08:00 | 执行者=wyf | 动作=docker.perm.add | 目标=lisi | 详情=加入 docker 组（等价 root 权限）
2026-07-25T22:15:21+08:00 | 执行者=unknown | 动作=user.del.purge | 目标=zhangsan | 详情=释放 1.9GB（家目录 + 3 个数据盘目录）
2026-07-25T22:15:21+08:00 | 执行者=unknown | 动作=uninstall | 目标=- | 详情=清理二进制/cron/MOTD 注入，保留数据与配置
```

**"执行者"取 `SUDO_USER`**，所以管理员用 `sudo` 执行时记的是本人（上面的 `执行者=wyf`）；
直接以 root 身份跑（如 cron、`docker exec`）且没有 `USER`/`LOGNAME` 时记为 `unknown`。

过滤：

```
$ /usr/local/bin/server-mgr audit --user zhangsan          # 执行者或目标匹配
2026-07-25T22:14:59+08:00 | 执行者=unknown | 动作=user.add | 目标=zhangsan | ...
2026-07-25T22:15:21+08:00 | 执行者=unknown | 动作=user.del.purge | 目标=zhangsan | ...

$ /usr/local/bin/server-mgr audit --since 2026-07-25       # 也支持 "2026-07-25 12:00:00"
$ /usr/local/bin/server-mgr audit --since 2030-01-01
没有符合条件的审计记录

$ /usr/local/bin/server-mgr audit --since 昨天
错误: --since 时间格式无效（用 2006-01-02 或 "2006-01-02 15:04:05"）: 无法解析 "昨天"
[退出码 1]
```

没有任何记录时输出 `暂无审计记录（还没有执行过写操作，或日志尚未生成）`。
**`uninstall` 不会删审计日志**，卸载后仍可用 `./server-mgr audit` 查看。

---

## Docker 管理

### `docker check` / `docker install`

**输入**：`server-mgr docker check`（任意用户）、`sudo server-mgr docker install`（需要 root，联网）。

```
$ /usr/local/bin/server-mgr docker check
Docker 已安装：Docker version 29.1.3, build 29.1.3-0ubuntu3~24.04.2

$ /usr/local/bin/server-mgr docker install
Docker 已安装（Docker version 29.1.3, build 29.1.3-0ubuntu3~24.04.2），无需重复安装
```

未安装时 `check` 会打印手动安装命令（BFSU 镜像源）并提示 `sudo server-mgr docker install`。
`docker install` 走官方 install.sh + `DOWNLOAD_URL=https://mirrors.bfsu.edu.cn/docker-ce`
（**本次未实测联网安装路径**，环境里 Docker 已装）。

### `docker perm` — 免 sudo 用 docker 的权限

**输入**：

- `server-mgr docker perm`（任意用户）：列出各普通用户的权限
- `sudo server-mgr docker perm add <用户名>`：加入 docker 组，**有风险提示 + y/N 确认**
- `sudo server-mgr docker perm del <用户名>`：移出 docker 组

```
$ /usr/local/bin/server-mgr docker perm
用户名       UID   Docker 权限  备注
ubuntu    1000  无权限        可执行: sudo server-mgr docker perm add ubuntu
zhangsan  1002  无权限        可执行: sudo server-mgr docker perm add zhangsan
lisi      1003  有权限        已加入 docker 组

$ /usr/local/bin/server-mgr docker perm add lisi
即将把用户 lisi 加入 docker 组。
⚠ docker 组成员可挂载宿主机任意目录并以 root 身份读写，等同于授予 root 权限。
确认授权? (y/N): 用户 lisi 已加入 docker 组
提示: 该用户需重新登录后才会生效（当前会话可执行 newgrp docker 临时生效）

$ /usr/local/bin/server-mgr docker perm del lisi
用户 lisi 已移出 docker 组
提示: 该用户已登录的会话仍持有旧的组身份，需重新登录后才彻底失效
```

重复操作是幂等的（`已在 docker 组中，无需重复添加` / `不在 docker 组中，无需操作`）；
输入 `n` 则 `已取消`；用户不存在时 `错误: 用户 nosuchuser 不存在`，退出码 1。

### `docker mirror set` — 镜像加速

**输入**：`sudo server-mgr docker mirror set [镜像地址...]`。**需要 root。**
不带参数用内置默认地址（编译期由 `.env` 的 `DOCKER_MIRRORS` 注入，当前是 `https://docker.1ms.run`）。
写完询问是否重启 Docker（会中断运行中的容器）。

```
$ /usr/local/bin/server-mgr docker mirror set
原配置已备份: /etc/docker/daemon.json.bak
镜像地址已写入 /etc/docker/daemon.json：
  https://docker.1ms.run

需要重启 Docker 守护进程后配置才生效。
⚠ 重启会中断正在运行的容器（带重启策略的容器会自动拉起）。
现在重启 Docker? (y/N): 已跳过。稍后可手动执行: sudo systemctl restart docker
```

**`daemon.json` 里已有的其他配置项原样保留**（实测）：

```
$ cat /etc/docker/daemon.json          # 改之前
{
  "log-driver": "json-file",
  "data-root": "/data/docker"
}

$ cat /etc/docker/daemon.json          # 改之后
{
  "data-root": "/data/docker",
  "log-driver": "json-file",
  "registry-mirrors": [
    "https://docker.1ms.run",
    "https://hub.example.com"
  ]
}
```

（JSON 键在重新编码后按字母序排列，值不受影响。）地址格式非法时拒绝写入：

```
$ /usr/local/bin/server-mgr docker mirror set docker.1ms.run
错误: 镜像地址必须以 http:// 或 https:// 开头: docker.1ms.run
[退出码 1]
```

---

## APT 源管理

### `source show`

**输入**：`server-mgr source show`，任意用户。识别当前镜像站并打印源文件全文。

```
$ /usr/local/bin/server-mgr source show
源文件: /etc/apt/sources.list.d/ubuntu.sources
当前镜像源: Ubuntu 官方

--- /etc/apt/sources.list.d/ubuntu.sources ---
...
Types: deb
URIs: http://archive.ubuntu.com/ubuntu/
Suites: noble noble-updates noble-backports
Components: main universe restricted multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg
...

备份文件存在: /etc/apt/sources.list.d/ubuntu.sources.bak      ← 有备份时才出现
```

可识别：阿里云 / 清华大学 / 中科大 / 北京外国语大学 / Ubuntu 官方，其余显示"未知"。

### `source set <mirror>`

**输入**：`sudo server-mgr source set aliyun|tsinghua|ustc|bfsu|official`。**需要 root。**
先备份到 `ubuntu.sources.bak`，写 DEB822 格式新源（codename 取自 `/etc/os-release`），
然后自动跑 `apt-get update`。

```
$ /usr/local/bin/server-mgr source set tsinghua
已备份原始源至 /etc/apt/sources.list.d/ubuntu.sources.bak
已切换至 清华大学（Ubuntu noble）

正在执行 apt-get update ...
Get:1 https://mirrors.tuna.tsinghua.edu.cn/ubuntu noble InRelease [256 kB]
...
Fetched 31.8 MB in 3s (10.7 MB/s)
Reading package lists...

换源完成。

$ /usr/local/bin/server-mgr source set nosuchmirror
错误: 不支持的镜像源 "nosuchmirror"，可选: aliyun / tsinghua / ustc / bfsu / official
[退出码 1]
```

### `source restore`

**输入**：`sudo server-mgr source restore`。从 `.bak` 还原并跑 `apt-get update`。

```
$ /usr/local/bin/server-mgr source restore
已从 /etc/apt/sources.list.d/ubuntu.sources.bak 还原

正在执行 apt-get update ...
...
还原完成。
```

无备份时：`错误: 备份文件 /etc/apt/sources.list.d/ubuntu.sources.bak 不存在，无法还原`，退出码 1。

---

## 配置文件

`/usr/local/lib/server-mgr/config.conf`（0644，`install` 时按默认值生成，之后**不会被覆盖**）。
格式是 shell 可 `source` 的 `KEY=VALUE`，每日统计脚本直接读同一个文件。

```
# server-mgr 配置文件
# 由 server-mgr install 生成，可直接编辑；格式为 shell 可 source 的 KEY=VALUE。
# 改动后需重新执行对应的 enable 命令才会更新 /etc/cron.d 下的定时任务。

# 分区使用率警戒线（百分比）。超过后 disk / MOTD 输出标红，
# 并在 MOTD 顶部醒目提示该分区
DISK_WARN_PERCENT=80

# 单用户总占用告警线（GB）。每日统计跑完后，占用超过此值的用户
# 会被 disk warn 点名写入 MOTD 警告
DISK_USER_WARN_GB=500

# /var/log/disk-usage 下每日报表保留天数
DISK_LOG_KEEP_DAYS=30

# 每日磁盘统计执行时间，格式 "分 时"
DISK_CRON_TIME="0 1"

# 每日不活跃用户检查执行时间，格式 "分 时"
INACTIVE_CRON_TIME="0 2"

# 超过多少天未登录视为不活跃用户
INACTIVE_DAYS=180
```

非法值会**静默回落到默认值**（MOTD 渲染路径不能因为配置写坏而中断登录）。
改了 `*_CRON_TIME` 要重新执行对应的 `enable` 才会更新 `/etc/cron.d` 下的文件。

告警推送配置单独放 `notify.conf`（0600，含密钥），见 [notify](#主动告警-notify)。

---

## 落盘路径总表

| 路径 | 内容 | `uninstall` 后 |
|---|---|---|
| `/usr/local/bin/server-mgr` | 二进制 | 删除 |
| `/usr/local/bin/disk-usage` | `disk usage` 快捷命令 | 删除 |
| `/usr/local/lib/server-mgr/config.conf` | 主配置 | **保留** |
| `/usr/local/lib/server-mgr/daily-disk-monitor.sh` | 每日统计脚本 | **保留** |
| `/usr/local/lib/server-mgr/motd/header.txt` | 欢迎语 | **保留** |
| `/usr/local/lib/server-mgr/motd/disabled-scripts.txt` | 被禁用的默认 MOTD 脚本清单 | 删除（恢复后） |
| `/usr/local/lib/server-mgr/motd/warnings.d/10-disk.txt` | 磁盘占用告警 | **保留** |
| `/usr/local/lib/server-mgr/motd/warnings.d/20-inactive.txt` | 不活跃用户告警 | **保留** |
| `/usr/local/lib/server-mgr/motd/init/*.sh` | Miniforge / uv 安装卸载脚本 | **保留** |
| `/usr/local/lib/server-mgr/notify.conf` | 推送配置（0600，含密钥） | **保留** |
| `/usr/local/lib/server-mgr/notify/state` | 告警去重状态（0600） | **保留** |
| `/etc/cron.d/server-mgr-disk` | 每日磁盘统计 | 删除 |
| `/etc/cron.d/server-mgr-inactive` | 每日不活跃检查 | 删除 |
| `/etc/update-motd.d/99-lab-info` | 自定义 MOTD 脚本 | 删除 |
| `/etc/bash.bashrc`、`/etc/zsh/zshrc` | VSCode MOTD 注入片段 | 片段移除 |
| `/var/log/disk-usage/current-usage.txt` | 最新统计报表 | **保留** |
| `/var/log/disk-usage/disk-usage-YYYY-MM-DD.log` | 每日归档（保留 `DISK_LOG_KEEP_DAYS` 天） | **保留** |
| `/var/log/disk-usage/cron.log` | cron 执行日志 | **保留** |
| `/var/log/server-mgr/audit.log` | 写操作审计（0600） | **保留** |
| `/etc/docker/daemon.json{,.bak}` | 镜像加速 | 不动 |
| `/etc/apt/sources.list.d/ubuntu.sources{,.bak}` | APT 源 | 不动 |

---

## 定时任务

| 文件 | 默认时间 | 干什么 |
|---|---|---|
| `/etc/cron.d/server-mgr-disk` | 每天 01:00 | 跑统计脚本 → 顺带 `disk warn` + `notify check` |
| `/etc/cron.d/server-mgr-inactive` | 每天 02:00 | `user inactive warn` |

时间取自 `config.conf` 的 `DISK_CRON_TIME` / `INACTIVE_CRON_TIME`（格式 `"分 时"`），
改完要重新 `enable` 一次。

---

## 测试环境（怎么复现）

命令会建删用户、改 `/etc/cron.d`、动 MOTD 和 `/etc/apt`，**不能在开发机或真实服务器上试跑**。
`test/` 下是一次性的 Docker 演练环境：

```bash
./test/run-e2e.sh                 # 编译 → 建镜像 → 跑完全部命令，日志写到 /tmp
./test/run-e2e.sh -o out.log      # 指定日志路径
./test/run-e2e.sh --shell         # 只布置环境，进容器手动折腾
```

| 文件 | 作用 |
|---|---|
| `test/Dockerfile` | Ubuntu 24.04 + cron/sudo/procps/iproute2/zsh/docker CLI |
| `test/setup-env.sh` | 在容器里模拟硬盘、挂载点、`/sys/block`、默认 MOTD 脚本、老用户 |
| `test/e2e.sh` | 逐条执行全部命令并记录输入输出（本文档的输出来源） |
| `test/fake-nvidia-smi` | 假 `nvidia-smi`，验证 GPU 各分支（正常 / 空闲 / 五类报错 / 挂起超时） |
| `test/fake-utmp.py` | 写 utmp 登录记录，让 `who` 有输出 |
| `test/fake-wechat.py` | 假企业微信 Webhook，验证推送链路而不打真实地址 |

**模拟了什么**（只模拟 server-mgr 读取的系统状态，工具自身逻辑全走真实代码）：

- **三块物理硬盘 + 五个挂载点**：`mount -t tmpfs /dev/sdX /挂载点` —— mount 的 source 字段
  会原样落进 `/proc/mounts`，于是多盘识别、按物理盘分组、`statfs` 容量统计全部走真实路径

  ```
  /dev/sda1 → /boot         (2 GB)      \_ sda：240 GB
  /dev/sda2 → /home         (236 GB)    /
  /dev/sdb1 → /data         (2046 GB)   \_ sdb：2 TB
  /dev/sdb2 → /workspace    (2 GB)      /   （故意做小，方便撑到警戒线以上）
  /dev/sdc1 → /mnt/storage  (1024 GB)   —— sdc：1 TB
  ```

- **`/sys/block`**：bind mount 一份只含 `sda/sdb/sdc` 的假目录，提供整盘扇区数
- **系统默认 MOTD 脚本**：官方镜像里被清空了，补两个仿真的用来验证禁用与恢复
- **一个 400 天没登录的账号**：直接写 `/var/log/lastlog` 的二进制记录
- **登录会话**：直接写 `/var/run/utmp`（容器里没有 sshd）
- **`/var/run/reboot-required`、`updates-available`**：验证系统提醒与推送
- **企业微信 Webhook**：本地 HTTP 服务，按真实响应格式回 `{"errcode":0,"errmsg":"ok"}`

容器需要 `--cap-add SYS_ADMIN`（挂载模拟盘），`--rm` 退出即净，不改开发机任何状态。

### 单测层的分工

`go test ./...`（93 个用例含子测试，当前全绿）覆盖的是**纯函数**：`/proc/mounts` 解析、
物理盘名推断、容量格式化、配置读写往返、`nvidia-smi` CSV 解析与故障分类、`/proc/<pid>` 解析、
GPU 按用户聚合、磁盘报表解析与超标筛选、MOTD 告警分文件读写与迁移、APT 镜像识别、
Docker `daemon.json` 合并等。

本报告不重复这些，专攻单测够不到的部分：真的装到 `/usr/local/bin`、真的写 `/etc/cron.d`、
真的建删用户与符号链接、真的改 `/etc/bash.bashrc`（并验证 `bash -n` 通过）、
命令的退出码与非 root 拒绝行为、`install → 用 → uninstall` 的完整生命周期。

---

## 已知问题与待补测

1. **GPU 的两个小分支只有桩验证** —— 实机上覆盖了 `gpu status`、`gpu top`（空闲 +
   两卡各一进程）、MOTD 段落、耗时；没覆盖到的是"进程属主读不到 → 未知"
   和"root 用户的 GPU 进程"这两种情况，它们只有桩数据。七类故障诊断同理
   （在实机上验证要真把驱动弄坏，不值得）。
2. **推送文案里的用量被四舍五入** —— `notify` 的分区告警正文写的是
   `已用 2/2 GB`（实际 1.70/2.00 GB），百分比是准的。标题与 MOTD 显示不受影响。
3. **`docker install` 的联网安装路径未实测** —— 采集环境里 Docker 已安装，
   只覆盖了"已安装则跳过"的分支。
4. **邮件渠道未实测** —— 只验证了企业微信 Webhook 链路（本地假端点）。
   SMTP 的 starttls / ssl / none 三种模式需要一个可用的 SMTP 服务器才能端到端验证。
5. **cron 未真正等到执行** —— 容器里没起 cron 守护进程，验证的是写出的 cron 文件内容
   与手动触发的效果（`disk monitor run`、`user inactive warn`）。

已修复（均已在容器 / 实机上复验）：

- `user who` 在 C/POSIX locale 下把来源列显示成时间、时长显示成 `?`
  （`who` 的时间格式随 locale 变成三段无年份的 `Jul 25 19:49`）——
  现在两种格式都解析，跨年场景也覆盖了单测。
- CUDA 版本行在新驱动上带出弃用说明：610.43.02 的 `nvidia-smi -q` 把
  `CUDA Version` 标成 `13.3 [Deprecated; will be removed in CUDA 14.0. …]`，
  原样打进了输出。现在优先读新键 `CUDA UMD Version`，回落旧键，并去掉方括号说明。
