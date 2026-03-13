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

**交互流程：**

1. 校验用户名格式（小写字母、数字、`_`、`-`）
2. 检查用户名是否已存在（`id <用户名>`）
3. 交互读取全名（写入 GECOS，不可为空）
4. 扫描数据盘候选列表（见 [disk-interface.md](./disk-interface.md) 的过滤规则）
5. 展示所有将要操作的路径，等待确认
6. 执行：
   - `useradd -m -s /bin/bash -c <全名> <用户名>`
   - 对每个数据盘挂载点：`mkdir <挂载点>/<用户名>` + `chown` + `ln -s` + `chown -h`
   - `chage -d 0 <用户名>`（强制首次登录改密）
   - `passwd <用户名>`（交互式设置初始密码）

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

---

## 扩展建议

- **新增用户字段**：在 `user add` 的 `useradd` 调用中追加参数即可，无需修改数据结构
- **数据盘过滤调整**：修改 `cmd/user.go` 中的 `systemMountExact` / `systemMountPrefixes` 变量
- **符号链接命名**：当前取挂载点 basename；若将来需自定义，可在 `user add` 增加 `--link-name` flag
