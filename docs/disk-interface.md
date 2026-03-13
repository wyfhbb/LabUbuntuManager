# 磁盘查询接口文档

本文档定义 `server-mgr disk` 及用户管理模块共用的磁盘查询契约。

## 数据结构

### DiskUsage — 单个挂载点

```go
type DiskUsage struct {
    Device      string  // 设备路径，来源于 /proc/mounts，前缀为 /dev/
    MountPoint  string  // 挂载点路径
    TotalGB     float64 // 总容量，GB（1024 进制）
    UsedGB      float64 // 已使用，GB
    FreeGB      float64 // 剩余，GB
    UsedPercent float64 // 使用率百分比，如 83.12
}
```

### PhysicalDisk — 物理磁盘（含分区列表）

```go
type PhysicalDisk struct {
    Name       string      // 物理盘路径，如 /dev/sda
    TotalGB    float64     // 物理容量（来自 /sys/block/<disk>/size），0 表示读取失败
    Partitions []DiskUsage // 该盘下所有已挂载分区，按挂载点升序
}
```

## 查询接口

```go
type DiskUsageProvider interface {
    ListDiskUsage() ([]DiskUsage, error)
}
```

**约定：**
- 仅返回设备路径以 `/dev/` 开头的挂载记录
- 自动排除 `loop*` 设备、`squashfs` 文件系统、`/snap/` 挂载点
- 结果按挂载点升序排列

**默认实现：** `cmd/disk.go` 中的 `ProcMountDiskUsageProvider`，数据源为 `/proc/mounts`。

## 辅助函数

| 函数 | 签名 | 用途 |
|---|---|---|
| `groupByPhysicalDisk` | `([]DiskUsage) []PhysicalDisk` | 按物理盘分组，读取 `/sys/block/` 获取物理容量 |
| `dataMountCandidates` | `([]DiskUsage) []DiskUsage` | 过滤出可供用户使用的数据盘挂载点（排除系统盘） |
| `physicalDiskName` | `(device string) string` | 从分区路径推断物理盘路径（`sda1`→`sda`，`nvme0n1p1`→`nvme0n1`） |
| `formatCapacityByGB` | `(float64) string` | 容量格式化，自动换算 MB / GB / TB |

## 数据盘过滤规则（dataMountCandidates）

以下挂载点**不会**出现在数据盘候选列表中：

精确排除：`/`、`/boot`、`/boot/efi`、`/home`

前缀排除：`/proc`、`/sys`、`/dev`、`/run`、`/tmp`、`/snap`

**用途：** `user add` 建立工作目录时的挂载点候选，`user list` 检测用户数据目录时的扫描范围。

## CLI 输出规则（server-mgr disk）

按物理盘分组展示，每组标题行 + 分区明细：

```
● /dev/sda  [500.00 GB 物理容量]
  /dev/sda1  /boot  总 512.00 MB  已用 150.00 MB  剩 362.00 MB  29.3%
  /dev/sda2  /      总 100.00 GB  已用 45.00 GB   剩 55.00 GB   45.0%

● /dev/sdb  [2.00 TB 物理容量]
  /dev/sdb1  /home  总 2.00 TB    已用 400.00 GB  剩 1.60 TB    20.0%
```

- 物理容量读取失败时标题行仅显示设备名，不报错
- 使用率 > 80% 时行尾追加 ` [!]`
