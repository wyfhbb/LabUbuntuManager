# AGENTS.md

## 项目概述

`server-mgr` 是一个用 Go 编写的 Ubuntu 服务器管理 CLI 工具，面向实验室新人，让常见运维操作标准化、傻瓜化。使用 `cobra` 框架，编译产物为单一二进制 `server-mgr`。

## 已实现功能

| 子命令 | 功能 |
|---|---|
| `disk` | 列出真实磁盘挂载点及容量（自动过滤 snap/loop） |
| `disk usage` | 查看各用户当日磁盘使用量（所有用户可用，无需 root） |
| `disk monitor enable` | 启用每日磁盘用量统计（安装二进制、快捷命令 `disk-usage`、cron job，需要 root） |
| `disk monitor disable` | 禁用定时任务并删除快捷命令（需要 root） |
| `disk monitor status` | 查看定时任务状态及最近统计时间 |
| `disk monitor run` | 立即执行一次统计（需要 root） |
| `source show` | 查看当前 APT 镜像源 |
| `source set <mirror>` | 切换 APT 源（aliyun / tsinghua / ustc / bfsu / official） |
| `source restore` | 从备份还原 APT 源 |

## 规划中功能（待实现）

- `user list / add / del / passwd` — 用户管理
- `install` — 安装基础软件（vim、git、htop 等）
- `motd` — 替换 MOTD 欢迎信息
- `disk alert` — 磁盘容量超阈值告警（邮件 / 钉钉等）

## 项目结构

```
.
├── main.go
├── bashscr          # 以前的脚本，仅用作兼容性参考，不修改，不删除，不使用这里面的脚本
├── shell            # 用作存放一些必要的生成的脚本的模板
├── cmd/
│   ├── root.go           # rootCmd 定义，Execute() 入口
│   ├── disk.go           # disk 子命令（挂载点/容量展示）
│   ├── disk_monitor.go   # disk monitor / disk usage 子命令
│   ├── shell/            # go:embed 嵌入的 shell 脚本（随二进制一同编译）
│   │   └── daily-disk-monitor.sh
│   └── source.go         # source 子命令组
├── bashscr/         # 历史遗留 bash 脚本，仅参考，勿直接调用
└── go.mod
```

## 技术约束

- **依赖**：仅允许 Go 标准库 + `github.com/spf13/cobra`，禁止引入其他第三方包
- **目标 OS**：Ubuntu Linux，不需要跨平台兼容
- **子命令注册**：每个文件在自己的 `init()` 中向 `rootCmd` 注册子命令

## 编码规范

- 错误输出到 `os.Stderr`，致命错误 `os.Exit(1)`
- 需要 root 的命令：操作前先检查 `os.Getuid() == 0`，否则打印提示后退出
- 调用系统命令：`os/exec`，设置 `cmd.Stdout = os.Stdout` / `cmd.Stderr = os.Stderr` 实现实时流式输出
- 禁止使用 `log` 包；错误统一用 `fmt.Fprintf(os.Stderr, ...)`
- 中文界面，错误信息前缀统一为 `"错误: "`

## 关键实现细节

### disk.go
- 解析 `/proc/mounts`，过滤 `loop` 设备、`squashfs` 文件系统、`/snap/` 挂载点
- 用 `syscall.Statfs` 获取容量，使用率 > 80% 输出 `[!]` 警告标记
- 容量自动格式化：MB / GB / TB

### source.go
- 源文件路径：`/etc/apt/sources.list.d/ubuntu.sources`（DEB822 格式）
- 写入前自动备份为 `.bak`，`restore` 子命令可还原
- 从 `/etc/os-release` 读取 `VERSION_CODENAME` 填充模板

### disk_monitor.go
- `disk monitor enable`（root）：写监控脚本到 `/usr/local/lib/server-mgr/daily-disk-monitor.sh`；写 `/etc/cron.d/server-mgr-disk`（每天 01:00）；将当前二进制复制到 `/usr/local/bin/server-mgr`；写快捷脚本 `/usr/local/bin/disk-usage`（任意用户可直接运行）；立即执行一次统计；打印使用说明
- `disk monitor disable`（root）：删除 cron 文件 + 快捷命令 `/usr/local/bin/disk-usage`；保留历史数据
- `disk monitor run`（root）：立即执行一次统计（不改变 cron 开关状态）
- `disk monitor status`：显示 cron 开关状态、快捷命令是否安装、最近统计时间
- `disk usage`（所有用户）：第一部分展示系统磁盘挂载点使用情况（复用 disk.go 的 `ProcMountDiskUsageProvider` + `renderPhysicalDisks`）；第二部分读取 `/var/log/disk-usage/current-usage.txt` 展示各用户使用量；TAB 分隔格式解析，`tabwriter` 对齐；`--me` / `-m` 仅显示当前用户；通过 `SUDO_USER` 识别真实用户
- 统计脚本逻辑（`cmd/shell/daily-disk-monitor.sh`）：读 `/etc/passwd` 获取 UID≥1000 且 shell 可登录的用户；`du -sk` 计算 home 目录 + `/workspace/<user>` + `/data/<user>`；输出文件：`# generated:` 注释行 + TAB 分隔数据行（username / home_gb / total_gb / full_name）；`chmod 644` 保证所有用户可读

### user.go（待实现）
- `user list`：解析 `/etc/passwd`，过滤 UID ≥ 1000 且 shell ≠ `/usr/sbin/nologin`，用 `text/tabwriter` 对齐输出
- `user add <username>`：解析 `/proc/mounts` 找 `/dev/` 前缀挂载点 → `syscall.Statfs` 查空间 → 编号交互选择挂载位置 → `useradd -m -d <mount>/<username> -s /bin/bash` → `chage -d 0` 强制首次登录改密
- `user del <username>`：`userdel <username>`；`--purge` 标志时加 `-r` 删主目录
- `user passwd <username>`：`passwd <username>`

## 构建与测试

```bash
make build        # 编译到 ./server-mgr
sudo ./server-mgr disk
sudo ./server-mgr source set aliyun
```

- 仅人工测试，无需编写单元测试文件（除非明确要求）
- 需要 root 或 `sudo` 执行
