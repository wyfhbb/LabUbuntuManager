# 用户管理接口文档

本文档定义 `server-mgr user` 子命令组的行为契约，供后续扩展参考。

## 子命令总览

| 子命令 | 需要 root | 说明 |
|---|---|---|
| `user list` | 否 | 列出所有普通用户及数据目录 |
| `user add <用户名>` | 是 | 创建用户，在各数据盘建立工作目录和符号链接 |
| `user del <用户名>` | 是 | 删除账号（保留文件） |
| `user del --purge <用户名>` | 是 | 删除账号 + 家目录 + 各数据盘目录 |
| `user passwd <用户名>` | 是 | 交互式修改用户密码 |
| `user who` | 否 | 当前登录会话（SSH 来源 IP、时长）+ 推断的 VSCode Remote 会话 |
| `user top` | 否 | 按用户聚合的 CPU/内存排行，标出长期占用大内存的进程 |

写操作类子命令（`user add` / `del` / `passwd` / `inactive purge`）成功后会写入审计日志
`/var/log/server-mgr/audit.log`，实测记录见 [端到端实测报告](./e2e-report.md#审计-audit)。

---

## user list

**数据来源：** 解析 `/etc/passwd`

**过滤规则：** UID ≥ 1000，shell ≠ `/usr/sbin/nologin` 且 ≠ `/bin/false`

**输出列：**

| 列 | 来源 |
|---|---|
| 用户名 | `/etc/passwd` 第 1 列 |
| UID | `/etc/passwd` 第 3 列 |
| 全名 | GECOS 字段（第 5 列），逗号前部分 |
| 主目录 | `/etc/passwd` 第 6 列 |
| 数据目录 | 扫描各数据盘挂载点下 `<挂载点>/<用户名>` 是否存在，存在则列出，多个以 `, ` 分隔，无则显示 `-` |

**示例输出：**
```
用户名    UID   全名  主目录          数据目录
zhangsan  1001  张三  /home/zhangsan  /workspace/zhangsan, /data/zhangsan
lisi      1002  李四  /home/lisi      /workspace/lisi
wangwu    1003        /home/wangwu    -
```

---

## user add

**设计原则：先收集校验全部输入，再改系统（事务性）。** 所有交互输入（全名、密码）
和路径预检都在动手前完成；进入执行阶段后任一步失败即回滚本次已创建的内容，
回滚本身若再失败则明确列出残留路径与手动清理命令，绝不留下半成品用户。

**交互流程：**

1. 校验用户名格式（小写字母、数字、`_`、`-`）
2. 检查用户名是否已存在（`id <用户名>`）
3. **预检家目录 `/home/<用户名>` 不存在**——已存在则中止（不改动它），
   否则回滚阶段的 `userdel -r` 会误删非本次创建的目录
4. 扫描数据盘候选列表（见 [disk-interface.md](./disk-interface.md) 的过滤规则）
5. **预检每块数据盘上 `<挂载点>/<用户名>` 均不存在**——任一存在则中止（不改动它）
6. 交互读取全名（写入 GECOS，不可为空）
7. 交互读取初始密码（隐藏输入，两次一致即可，**允许弱密码**；不一致只重新提示，不触碰系统）
8. 展示所有将要操作的路径，等待确认
9. 执行（任一步失败即回滚）：
   - `useradd -m -s /bin/bash -c <全名> <用户名>`
   - 对每个数据盘挂载点：创建前再确认目录不存在 → `mkdir <挂载点>/<用户名>`（记入回滚清单）
     + `chown` + `ln -s` + `chown -h`
   - `chpasswd`（以 root 设置初始密码，**不经 PAM pwquality，故弱密码可用**；不强制首次登录改密）

**回滚规则：** 逆序清理——先删本次真正 `mkdir` 出来的数据盘工作目录（逆序），
再 `userdel -r <用户名>`（一并清掉家目录及其中的符号链接）。只删本次创建成功的路径，
已存在的同名目录一律不碰。回滚由纯函数 `rollbackUserAdd` 编排（删除动作注入以便单测）。

**目录与符号链接规则：**

| 路径 | 说明 |
|---|---|
| `/home/<用户名>` | 家目录，由 `useradd -m` 自动创建 |
| `<挂载点>/<用户名>` | 数据工作目录，权限 `700`，归属用户 |
| `/home/<用户名>/<挂载点basename>` | 符号链接，指向上一行，basename 取挂载点最后一段（如 `/workspace` → `workspace`） |

**全名存储：** 写入 `/etc/passwd` GECOS 字段，`getent passwd <用户名> | cut -d: -f5` 可读取，与 `bashscr/daily-disk-monitor.sh` 兼容。

---

## user del

| 调用方式 | 行为 |
|---|---|
| `user del <用户名>` | 仅调用 `userdel <用户名>`，保留所有文件 |
| `user del --purge <用户名>` | 调用 `userdel -r <用户名>`（含家目录），并删除各数据盘下 `<挂载点>/<用户名>` 目录 |

**注意：** `--purge` 时先收集数据盘目录路径，再执行 `userdel`，避免账号删除后无法查询挂载信息。

---

## user passwd

直接调用 `passwd <用户名>`，`Stdin/Stdout/Stderr` 均透传，完全交互式。
成功后写入审计日志（动作 `user.passwd`）。

---

## user who

**数据来源：** `who`（登录会话）+ `ps -eo user=,pid=,etimes=,args=`（VSCode Remote 推断）。

- 登录会话：解析 `who` 每行 `用户 tty 日期 时间 [(来源)]`，来源含 IP 者标为 `SSH`、
  否则标为 `本地`；登录时长 = 当前时间 − 登录时间
- VSCode Remote：不是登录会话，靠扫描各用户的 `.vscode-server` 进程**推断**，
  每个有该进程的用户算一个会话，取运行最久的进程代表会话起始，类型标注"(推断)"

解析函数 `parseWhoSessions` / `parseVscodeSessions` 为纯函数（有单测）。所有用户可用，无需 root。

---

## user top

**数据来源：** `ps -eo user=,pid=,%cpu=,rss=,etimes=,comm=`。

- 按属主聚合：每个用户的进程数、CPU 合计、常驻内存（RSS）合计，按内存降序
- 单独列出**长期占用大内存的进程**：RSS ≥ 8 GiB 且运行 ≥ 1 天（阈值为内置常量
  `bigMemRSSBytes` / `longRunSeconds`）

聚合函数 `aggregateUserProcesses` 为纯函数（有单测）。所有用户可用，无需 root。

---

## 扩展建议

- **新增用户字段**：在 `user add` 的 `useradd` 调用中追加参数即可，无需修改数据结构
- **数据盘过滤调整**：修改 `cmd/user.go` 中的 `systemMountExact` / `systemMountPrefixes` 变量
- **符号链接命名**：当前取挂载点 basename；若将来需自定义，可在 `user add` 增加 `--link-name` flag
