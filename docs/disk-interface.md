# 磁盘占用查询接口文档

本文档定义 `server-mgr disk` 使用的可复用磁盘占用查询契约，作为后续扩展的基础。

## 数据结构

`DiskUsage` 是单个真实磁盘挂载点的标准化模型。

```go
type DiskUsage struct {
    Device      string
    MountPoint  string
    TotalGB     float64
    UsedGB      float64
    FreeGB      float64
    UsedPercent float64
}
```

### 字段语义

- `Device`: 设备路径，来源于 `/proc/mounts`，预期前缀为 `/dev/`
- `MountPoint`: 挂载点路径
- `TotalGB`: 总容量，单位 GB（1024 进制）
- `UsedGB`: 已使用容量，单位 GB
- `FreeGB`: 剩余容量，单位 GB
- `UsedPercent`: 使用率百分比（例如 `83.12`）

## 查询接口

`DiskUsageProvider` 是高频查询场景的稳定访问接口。

```go
type DiskUsageProvider interface {
    ListDiskUsage() ([]DiskUsage, error)
}
```

### 接口约定

- 仅返回设备路径以 `/dev/` 开头的挂载记录
- 从 `/proc/mounts` 读取挂载信息
- 对每个挂载点调用 `syscall.Statfs` 获取容量统计
- 返回值为可直接渲染和业务消费的预计算结果

## 默认实现

`cmd/disk.go` 中的 `ProcMountDiskUsageProvider` 为默认实现。

- 挂载源：`/proc/mounts`
- 输出排序：按挂载点升序
- 单位换算：字节 -> GB（除数 `1024 * 1024 * 1024`）

## CLI 输出规则（`server-mgr disk`）

- 表头列：`设备` `挂载点` `总量` `已用` `剩余` `使用率`
- 输出格式：容量统一为 GB，保留两位小数
- 告警规则：当 `UsedPercent > 80` 时，在行尾追加 ` [!]`

## 后续扩展建议

- 字段扩展：优先在 `DiskUsage` 追加新字段，避免修改已有字段语义
- 接口扩展：新增能力时优先新增并行接口（如过滤、分页、附加统计），不直接破坏 `ListDiskUsage()` 签名
- 实现扩展：如需支持其他数据源，可新增 provider 实现并复用 `DiskUsageProvider` 接口
- 阈值扩展：告警阈值已在代码中独立常量化，后续可平滑改为配置项或命令行参数
