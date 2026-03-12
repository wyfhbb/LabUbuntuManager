package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

const bytesPerGB = 1024 * 1024 * 1024
const defaultDiskUsageWarnPercent = 80.0
const gbPerTB = 1024.0
const mbPerGB = 1024.0

var excludedDeviceBasePrefixes = []string{
	"loop", // snap 等系统镜像常见设备
}

var excludedFSTypes = map[string]struct{}{
	"squashfs": {}, // snap 挂载常见文件系统
}

var excludedMountPointPrefixes = []string{
	"/snap/", // snap 包挂载目录
}

// DiskUsage 表示单个真实磁盘挂载点的容量数据。
//
// 字段约定（面向后续高频查询）：
//   - Device: 设备路径，预期前缀为 "/dev/"。
//   - MountPoint: 挂载点路径（来自 /proc/mounts）。
//   - TotalGB: 总容量，单位 GB（1024 进制）。
//   - UsedGB: 已使用容量，单位 GB。
//   - FreeGB: 剩余容量，单位 GB。
//   - UsedPercent: 使用率百分比。
//
// 扩展建议：后续新增统计字段时，优先追加字段并保持现有字段语义不变。
type DiskUsage struct {
	Device      string
	MountPoint  string
	TotalGB     float64
	UsedGB      float64
	FreeGB      float64
	UsedPercent float64
}

// DiskUsageProvider 定义磁盘占用查询接口。
//
// ListDiskUsage 返回所有设备路径以 "/dev/" 开头的挂载点容量信息。
//
// 扩展建议：若后续需要分页、过滤或附加统计，可新增并行接口，避免破坏当前签名。
type DiskUsageProvider interface {
	ListDiskUsage() ([]DiskUsage, error)
}

// ProcMountDiskUsageProvider 基于 /proc/mounts 提供磁盘占用查询能力。
type ProcMountDiskUsageProvider struct {
	mountsPath string
}

// NewProcMountDiskUsageProvider 创建默认查询实现。
//
// 当前默认挂载信息来源为 /proc/mounts，后续可通过新增构造函数扩展数据源。
func NewProcMountDiskUsageProvider() *ProcMountDiskUsageProvider {
	return &ProcMountDiskUsageProvider{
		mountsPath: "/proc/mounts",
	}
}

// ListDiskUsage 实现 DiskUsageProvider。
func (p *ProcMountDiskUsageProvider) ListDiskUsage() ([]DiskUsage, error) {
	file, err := os.Open(p.mountsPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", p.mountsPath, err)
	}
	defer file.Close()

	usages, err := parseDiskUsageFromMounts(file)
	if err != nil {
		return nil, err
	}

	sort.Slice(usages, func(i, j int) bool {
		return usages[i].MountPoint < usages[j].MountPoint
	})
	return usages, nil
}

func parseDiskUsageFromMounts(r io.Reader) ([]DiskUsage, error) {
	var usages []DiskUsage

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}

		device := fields[0]
		mountPoint := decodeProcMountField(fields[1])
		fsType := fields[2]
		if !shouldIncludeMount(device, mountPoint, fsType) {
			continue
		}

		var stat syscall.Statfs_t
		if err := syscall.Statfs(mountPoint, &stat); err != nil {
			return nil, fmt.Errorf("statfs %s (%s): %w", mountPoint, device, err)
		}

		totalBytes := float64(stat.Blocks) * float64(stat.Bsize)
		freeBytes := float64(stat.Bavail) * float64(stat.Bsize)
		usedBytes := totalBytes - freeBytes

		var usedPercent float64
		if totalBytes > 0 {
			usedPercent = usedBytes / totalBytes * 100
		}

		usages = append(usages, DiskUsage{
			Device:      device,
			MountPoint:  mountPoint,
			TotalGB:     totalBytes / bytesPerGB,
			UsedGB:      usedBytes / bytesPerGB,
			FreeGB:      freeBytes / bytesPerGB,
			UsedPercent: usedPercent,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read /proc/mounts: %w", err)
	}

	return usages, nil
}

func shouldIncludeMount(device, mountPoint, fsType string) bool {
	if !strings.HasPrefix(device, "/dev/") {
		return false
	}

	deviceBase := filepath.Base(device)
	for _, prefix := range excludedDeviceBasePrefixes {
		if strings.HasPrefix(deviceBase, prefix) {
			return false
		}
	}

	if _, excluded := excludedFSTypes[fsType]; excluded {
		return false
	}

	for _, prefix := range excludedMountPointPrefixes {
		if strings.HasPrefix(mountPoint, prefix) {
			return false
		}
	}

	return true
}

func decodeProcMountField(raw string) string {
	replacer := strings.NewReplacer(
		`\\040`, " ",
		`\\011`, "\t",
		`\\012`, "\n",
		`\\134`, `\`,
	)
	return replacer.Replace(raw)
}

func renderDiskUsageTable(w io.Writer, usages []DiskUsage) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "设备\t挂载点\t总量\t已用\t剩余\t使用率"); err != nil {
		return err
	}

	for _, usage := range usages {
		warn := ""
		if usage.UsedPercent > defaultDiskUsageWarnPercent {
			warn = " [!]"
		}

		if _, err := fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\t%s\t%.2f%%%s\n",
			usage.Device,
			usage.MountPoint,
			formatCapacityByGB(usage.TotalGB),
			formatCapacityByGB(usage.UsedGB),
			formatCapacityByGB(usage.FreeGB),
			usage.UsedPercent,
			warn,
		); err != nil {
			return err
		}
	}

	return tw.Flush()
}

func formatCapacityByGB(valueGB float64) string {
	switch {
	case valueGB >= gbPerTB:
		return fmt.Sprintf("%.2f TB", valueGB/gbPerTB)
	case valueGB < 1:
		return fmt.Sprintf("%.2f MB", valueGB*mbPerGB)
	default:
		return fmt.Sprintf("%.2f GB", valueGB)
	}
}

var diskCmd = &cobra.Command{
	Use:   "disk",
	Short: "展示真实磁盘挂载点及容量信息",
	Run: func(cmd *cobra.Command, args []string) {
		provider := NewProcMountDiskUsageProvider()
		usages, err := provider.ListDiskUsage()
		if err != nil {
			fmt.Fprintf(os.Stderr, "查询磁盘占用失败: %v\n", err)
			os.Exit(1)
		}

		if len(usages) == 0 {
			fmt.Fprintln(os.Stdout, "未找到设备路径以 /dev/ 开头的挂载点")
			return
		}

		if err := renderDiskUsageTable(os.Stdout, usages); err != nil {
			fmt.Fprintf(os.Stderr, "输出磁盘占用失败: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(diskCmd)
}
