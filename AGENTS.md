# AGENTS.md

## 项目概述

`server-mgr` 是一个用 Go 编写的 Ubuntu 服务器管理 CLI 工具，面向实验室新人，让常见运维操作标准化、傻瓜化。使用 `cobra` 框架，编译产物为单一二进制 `server-mgr`。

## 已实现功能

| 子命令 | 功能 |
|---|---|
| `disk` | 列出真实磁盘挂载点及容量（自动过滤 snap/loop） |
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
├── cmd/
│   ├── root.go      # rootCmd 定义，Execute() 入口
│   ├── disk.go      # disk 子命令
│   └── source.go    # source 子命令组
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
