# 🎉 server-mgr v1.0.0

实验室 Ubuntu 服务器管理 CLI 的**首个正式版**。
一条命令回答"谁把盘塞满了、谁占着显卡、谁还在线、谁半年没露面"，
把建用户、换源、装 Docker、配登录欢迎语这些重复劳动一并收掉。

单个静态二进制，`scp` 过去就能跑，无运行时依赖。

---

## 📦 拿到手怎么用

```bash
# 1. 传到服务器
scp -P <端口> server-mgr <用户>@<服务器>:~/

# 2. 校验（可选）
sha256sum server-mgr    # 应为下方"构建信息"里的值

# 3. 安装
sudo ./server-mgr install

# 4. 按需开功能
sudo server-mgr disk monitor enable            # 每天 01:00 统计各用户占用 + 超标告警
sudo server-mgr motd set                       # 自定义登录欢迎信息（含 VSCode 终端）
sudo server-mgr user inactive monitor enable   # 每天 02:00 检查长期不登录用户
sudo server-mgr notify config                  # 企业微信 / 邮件推送
```

只想看不想装也行：`gpu status`、`disk`、`user who`、`user top`
这些只读命令拷过去直接跑就有输出，不需要 root，不留任何文件。

卸载：`sudo server-mgr uninstall`（历史统计、审计日志、配置按设计保留）。

---

## ✨ 这一版有什么

| 模块 | 命令 | 能干嘛 |
|---|---|---|
| 💾 **磁盘** | `disk` / `disk usage` / `disk warn` / `disk monitor` | 按物理盘分组看容量、各用户占用排行、超标点名进登录页、每日统计定时任务 |
| 🎮 **GPU** | `gpu status` / `gpu top` | 每张卡的显存/利用率/温度/功耗，进程**按用户聚合**（谁占着哪张卡），驱动异常给出七类诊断 |
| 👥 **用户** | `user list/add/del/passwd` / `who` / `top` / `inactive` | 建号自动开多盘工作目录+软链、中途失败自动回滚、谁在线（含 VSCode Remote）、谁吃内存、长期不登录清理 |
| 👋 **MOTD** | `motd set/show/status/reset` | 登录欢迎信息，磁盘/GPU/告警一屏看清，VSCode 终端同样显示 |
| 📣 **主动告警** | `notify config/test/check` | 分区超线、GPU 异常、需要重启三源推企业微信/邮箱，24h 去重 |
| 📜 **审计** | `audit` | 所有成功写操作留痕：谁、何时、动了谁、释放多少 |
| 🐳 **Docker** | `docker check/install/perm/mirror` | 装 Docker、收放免 sudo 权限（带 root 风险提示）、配镜像加速不破坏已有配置 |
| 📦 **APT 源** | `source show/set/restore` | 五个国内镜像站一键切换，自动备份 |

完整功能介绍见 [README](README.md)，每条命令的真实输入输出见
[端到端实测报告](docs/e2e-report.md)。

### 几个值得单说的设计

- 🛡️ **`user add` 是有事务性的**：先收齐并校验全部输入再动系统，
  中途任何一步失败都会回滚干净（实测：数据盘挂只读，失败后账号、家目录、
  各盘目录全无残留，审计里也不留记录）；已存在的同名目录一律不碰。
- ⏱️ **登录路径上的实时查询都有护栏**：分区用量 1 秒、GPU 2 秒。
  掉盘重试、`fsfreeze` 冻结、挂起的显卡驱动都不会把所有人挡在登录界面外。
- 🩺 **`nvidia-smi` 的报错会被翻译成人话**：最经典的
  `Driver/library version mismatch`（升级驱动后没重启）会直接给出结论、成因和处理步骤，
  而不是丢一句 `exit status 255`。
- 🧾 **审计记的是人不是 root**：执行者取 `SUDO_USER`，
  `sudo server-mgr user del --purge` 记下的是管理员本人。
- ♻️ **卸载不会把数据带走**：`/var/log/disk-usage`、`/var/log/server-mgr/audit.log`、
  `/usr/local/lib/server-mgr`（含欢迎语与推送配置）全部保留。

---

## ✅ 这一版验证到什么程度

| 层面 | 覆盖 |
|---|---|
| 单元测试 | 93 个用例（含子测试）全绿，覆盖纯函数层：`/proc/mounts` 解析、物理盘名推断、配置读写往返、`nvidia-smi` CSV 解析与故障分类、`/proc/<pid>` 解析、报表解析、告警分文件读写与迁移、`daemon.json` 合并等 |
| 端到端 | Docker 一次性容器里跑完**全部命令**（含交互式输入、错误分支、权限拒绝、`install → 用 → uninstall` 完整生命周期），模拟了 3 块物理硬盘 / 5 个挂载点 / `lastlog` / `utmp` / 需重启标记 |
| 实机 | GPU 部分在 2×RTX 4090（驱动 610.43.02，CUDA 13.3）的实体服务器上验证：`gpu status`、`gpu top`（空闲态 + 两卡各一进程）、MOTD 段落、查询耗时 0.27 秒 |
| 静态检查 | `gofmt -l .` 与 `go vet ./...` 无输出 |

一条命令复现全部实测：`./test/run-e2e.sh`

### ⚠️ 已知边界

- GPU 的**故障诊断分支**与"进程属主读不到"的情况只有桩验证——
  在实机上验证要真把驱动弄坏，不值当
- **邮件渠道**与 `docker install` 的**联网安装路径**尚未端到端实测
- 告警推送挂在每日统计之后，默认**一天一次**；要分钟级就给 `notify check`
  单挂一个短间隔 cron，代码不用改
- `notify` 的分区告警正文里用量做了四舍五入（`已用 2/2 GB`），百分比是准的

完整清单见[已知问题与待补测](docs/e2e-report.md#已知问题与待补测)。

---

## 🧱 构建信息

| | |
|---|---|
| 版本 | `v1.0.0` |
| commit | `6fdd91b` |
| 构建时间 | 2026-07-26 00:17:21 |
| 工具链 | go1.25.7，`GOOS=linux GOARCH=amd64 CGO_ENABLED=0` |
| 产物 | `server-mgr`，静态链接 ELF 64-bit，9.8 MiB（10,261,432 字节） |
| SHA-256 | `f75f35a59f69f6acb93853cb0781875ca9c14ff7f6769ddcc3c1c67b1fe0efd6` |

自己编也一样：`make build`（版本号由 `git describe` 注入，无需改代码）。

---

## 🖥️ 系统要求

- **Ubuntu 24.04 及更高版本**（`source` 换源依赖 24.04 起启用的 DEB822 格式
  `ubuntu.sources`，版本代号运行时读取，新版本无需改代码；其余命令对更早的版本同样适用）
- x86 与 arm64 等非 x86 架构均可，换源时自动区分 `ubuntu` / `ubuntu-ports` 路径
- 安装与写操作类命令需要 root；只读命令任意用户可用
- 可选外部依赖：`cron`（定时任务）、`zsh`（zsh 注入，没装会自动跳过）、
  `docker`（docker 子命令）、`nvidia-smi`（GPU 子命令，没有 N 卡时整段优雅跳过）

---

## 🔄 升级与回滚

- **升级**：把新二进制传上去跑 `sudo ./server-mgr install` 即可，幂等。
  配置文件 `config.conf`、欢迎语 `header.txt`、推送配置 `notify.conf` **都不会被覆盖**
- **回滚**：换回旧二进制再 `install` 一次；或 `sudo server-mgr uninstall` 后重装
- 定时任务的时间点改在 `config.conf`，改完重新执行一次对应的 `enable`

---

## 📚 文档

| 文档 | 内容 |
|---|---|
| [README.md](README.md) | 功能介绍、快速开始、命令速查 |
| [docs/e2e-report.md](docs/e2e-report.md) | 端到端实测报告：每条命令的真实输入输出、落盘路径、错误分支 |
| [docs/disk-interface.md](docs/disk-interface.md) | 磁盘查询接口契约 |
| [docs/user-interface.md](docs/user-interface.md) | 用户管理接口契约 |
| [test/](test/) | Docker 端到端演练环境 |
