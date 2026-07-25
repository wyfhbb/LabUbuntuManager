# 🔧 server-mgr v1.0.1

**修订版**，只动了 `source` 换源一处。起因是一个提问：换源命令是不是写死了
Ubuntu 24.04、以后的版本还能不能用。

结论是版本本身没问题——版本代号一直是运行时从 `/etc/os-release` 读的，
26.04 上跑也不用改代码。但顺着查出四个真实缺陷，这一版把它们修掉了。

其余模块（磁盘、GPU、用户、MOTD、告警、审计、Docker）**一行没动**，
功能全集见 [v1.0.0 发版说明](docs/history/release100.md)。

---

## 🐛 这一版修了什么

### 1. 非 x86 架构会写出打不开的源 🔴

国内镜像站的 arm64 / ppc64el 包放在 `ubuntu-ports` 路径下，和 x86 的 `ubuntu`
是两套目录。v1.0.0 的模板把 URI 写死成 `.../ubuntu`，在 ARM 机器上换完源
`apt-get update` 必然 404 —— 实验室要是有 Jetson 或 GH200 就会踩到。

现在用 `dpkg --print-architecture` 认架构（拿不到时回退到二进制自身的编译架构），
除 amd64 / i386 外一律改用 `ubuntu-ports`；官方源在非 x86 上整体切到
`ports.ubuntu.com/ubuntu-ports`（security 在 ports 上没有独立域名）。
换源时会把认到的架构打出来：

```
已切换至 清华大学（Ubuntu noble，架构 amd64）
```

### 2. 换源失败会留下坏源 🔴

新版本刚发布、镜像站还没同步时，写进去的源是打不开的。v1.0.0 在
`apt-get update` 失败后直接退出，坏源原地保留，用户得自己想起来去
`source restore`——在那之前这台机器装不了任何包。

现在失败会自动回滚成换源前的内容，并明确告知：

```
apt-get update 失败: exit status 100
已自动回滚 /etc/apt/sources.list.d/ubuntu.sources 至换源前的内容
```

回滚目标是**换源前**的内容而不是 `.bak`，所以连续换源时是退回上一步，
不会意外把机器打回出厂源。

### 3. 连续换源会冲掉出厂源备份 🟡

v1.0.0 每次 `set` 都覆盖 `.bak`。换两次源之后，`.bak` 里存的其实是上一个镜像站，
`restore` 就再也回不到出厂源了——但它还是照旧打印"已备份原始源"。

现在 `.bak` 已存在就保留不覆盖，`restore` 始终指向真正的出厂源：

```
备份 /etc/apt/sources.list.d/ubuntu.sources.bak 已存在，保留不覆盖
```

### 4. 系统版本不符时的报错看不懂 🟢

DEB822 格式是 Ubuntu 24.04 才启用的。在 22.04 上跑 `source show`，
v1.0.0 丢出来的是一句裸的 `no such file or directory`。现在直接说清原因：

```
错误: 未找到 /etc/apt/sources.list.d/ubuntu.sources
本命令依赖 Ubuntu 24.04 起启用的 DEB822 源格式；更早的版本用的是 /etc/apt/sources.list 单行格式，暂不支持
```

### 顺带

- 审计日志补记架构，失败的换源也留痕（v1.0.0 只在成功时记，且没有架构信息）：
  ```
  动作=source.set | 目标=清华大学 | 详情=codename=noble arch=amd64
  动作=source.set | 目标=阿里云   | 详情=失败并回滚，codename=nosuchrelease arch=amd64
  ```
- `source show` 认得出 `ports.ubuntu.com`，ARM 机上不再显示"未知"
- 五份重复的 DEB822 模板收敛成一张镜像源表 + 单一模板，加镜像站现在只改一处数据

---

## ⬆️ 怎么升级

```bash
scp -P <端口> server-mgr <用户>@<服务器>:~/
sudo ./server-mgr install
```

幂等，配置文件（`config.conf`、`header.txt`、`notify.conf`）都不会被覆盖。
**不需要重新配置任何东西**，定时任务、欢迎语、推送设置原样保留。

从 v1.0.0 升上来没有任何破坏性改动：命令、参数、输出格式、落盘路径全部不变，
唯一的可见差异是 `source set` 成功时多打印一个架构字段。

回滚：换回 v1.0.0 的二进制再 `install` 一次即可。

---

## ✅ 验证到什么程度

| 层面 | 覆盖 |
|---|---|
| 单元测试 | 97 个用例全绿（v1.0.0 为 93 个，本版新增 4 个测试函数）：架构分流 `usesPorts`、x86/ARM 两套 URI 渲染、codename 四处占位全部替换、镜像源表字段完整性 |
| 端到端 | `./test/run-e2e.sh` 全量演练重跑一遍，退出码 0，换源全流程（show / set tsinghua / restore / 非法镜像名）与其余模块均无回归 |
| 针对性验证 | 新增的三条分支在容器里逐条验证，13 项断言全通过：连续换源后 `.bak` 仍等于出厂源、伪造版本代号触发 `apt-get update` 失败后源文件按字节回到换源前、源文件缺失时的提示不再暴露 `no such file` |
| 静态检查 | `gofmt -l .` 与 `go vet ./...` 无输出 |

### ⚠️ 已知边界

- **arm64 路径只验证到生成的源文件内容，没在真 ARM 机上实测**。
  单测覆盖了 URI 分流和渲染结果，但"阿里云 ubuntu-ports 上确实有 noble 的包"
  这件事是查文档确认的，不是跑出来的。手上有 ARM 机器的话，
  `source set` 之后看一眼 `apt-get update` 就能确认。
- `dpkg` 不可用时回退到二进制自身的编译架构。用 amd64 的二进制跑在
  arm64 机器上（理论上不会发生，静态二进制跑不起来）会判断错。
- [端到端实测报告](docs/e2e-report.md) 里 `source` 一节的输出仍是 v1.0.0 的，
  少了架构字段和新增的两条分支，下次整体重录时再更新。
- v1.0.0 的已知边界（GPU 故障诊断只有桩验证、邮件渠道未端到端实测等）
  **全部继续适用**，本版没有涉及。

---

## 🧱 构建信息

| | |
|---|---|
| 版本 | `v1.0.1` |
| commit | `6d0d534` |
| 构建时间 | 2026-07-26 00:33:55 |
| 工具链 | go1.25.7，`GOOS=linux GOARCH=amd64 CGO_ENABLED=0` |
| 产物 | `server-mgr`，静态链接 ELF 64-bit，9.8 MiB（10,262,922 字节） |
| SHA-256 | `c97e0bf4f7ce720566c8ce490682caed454603ea57a853ee235648c2a311626e` |

自己编也一样：`make build`（版本号由 `git describe` 注入，无需改代码）。

---

## 🖥️ 系统要求

- **Ubuntu 24.04 及更高版本**（`source` 换源依赖 24.04 起启用的 DEB822 格式
  `ubuntu.sources`；版本代号运行时读取，新版本发布后无需改代码。
  其余命令对更早的版本同样适用）
- x86 与 arm64 等非 x86 架构均可，换源时自动区分 `ubuntu` / `ubuntu-ports` 路径
- 安装与写操作类命令需要 root；只读命令任意用户可用
- 可选外部依赖：`cron`（定时任务）、`zsh`（zsh 注入，没装会自动跳过）、
  `docker`（docker 子命令）、`nvidia-smi`（GPU 子命令，没有 N 卡时整段优雅跳过）

---

## 📚 文档

| 文档 | 内容 |
|---|---|
| [README.md](README.md) | 功能介绍、快速开始、命令速查 |
| [docs/e2e-report.md](docs/e2e-report.md) | 端到端实测报告：每条命令的真实输入输出、落盘路径、错误分支 |
| [docs/disk-interface.md](docs/disk-interface.md) | 磁盘查询接口契约 |
| [docs/user-interface.md](docs/user-interface.md) | 用户管理接口契约 |
| [test/](test/) | Docker 端到端演练环境 |

### 📁 历史版本

| 版本 | 说明 |
|---|---|
| [v1.0.0](docs/history/release100.md) | 首个正式版：磁盘 / GPU / 用户 / MOTD / 告警 / 审计 / Docker / APT 源全部功能 |
