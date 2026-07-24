package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

const bytesPerGB = 1024 * 1024 * 1024
const bytesPerSector = 512

// defaultDiskUsageWarnPercent 是分区使用率警戒线的出厂默认值，
// 实际取值见 config.conf 的 DISK_WARN_PERCENT。
const defaultDiskUsageWarnPercent = 80.0

// defaultDiskUserWarnGB 是单用户总占用告警线的出厂默认值，
// 实际取值见 config.conf 的 DISK_USER_WARN_GB。
const defaultDiskUserWarnGB = 500.0
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

// DiskUsage 表示单个挂载点的容量数据。
type DiskUsage struct {
	Device      string
	MountPoint  string
	TotalGB     float64
	UsedGB      float64
	FreeGB      float64
	UsedPercent float64
}

// PhysicalDisk 表示一块物理硬盘及其所有已挂载分区。
//
// TotalGB 来自 /sys/block/<disk>/size（内核直接暴露的扇区数），
// 为 0 表示读取失败（不影响分区容量展示）。
type PhysicalDisk struct {
	Name       string  // 物理盘路径，如 /dev/sda
	TotalGB    float64 // 物理容量，0 表示未知
	Partitions []DiskUsage
}

// DiskUsageProvider 定义磁盘占用查询接口。
type DiskUsageProvider interface {
	ListDiskUsage() ([]DiskUsage, error)
}

// ProcMountDiskUsageProvider 基于 /proc/mounts 提供磁盘占用查询能力。
type ProcMountDiskUsageProvider struct {
	mountsPath string
}

func NewProcMountDiskUsageProvider() *ProcMountDiskUsageProvider {
	return &ProcMountDiskUsageProvider{mountsPath: "/proc/mounts"}
}

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

// sysfsRoot 指向 sysfs 挂载点，测试时替换为夹具目录。
var sysfsRoot = "/sys"

// pStyleDiskPattern 匹配"整盘名 + 可选 p<数字> 分区后缀"的设备命名。
// 这类盘名本身以数字结尾（nvme0n1、mmcblk0），不能靠"去掉末尾数字"来推断整盘。
var pStyleDiskPattern = regexp.MustCompile(`^(nvme\d+n\d+|mmcblk\d+|loop\d+|md\d+|dm-\d+)(?:p\d+)?$`)

// physicalDiskName 从设备路径推断其所属的物理盘路径。
//
//	sda1        → /dev/sda
//	nvme0n1p1   → /dev/nvme0n1
//	nvme0n1     → /dev/nvme0n1（整盘直接挂载，不分区）
//	mmcblk0p1   → /dev/mmcblk0
//
// 以 sysfs 为准：/sys/block/<名字> 存在即整盘，分区目录下有 partition 文件、
// 其父目录就是所属整盘。sysfs 查不到时（夹具、异构设备）回落到命名规则。
func physicalDiskName(device string) string {
	base := filepath.Base(device)
	if name, ok := physicalDiskNameFromSysfs(base); ok {
		return "/dev/" + name
	}
	return "/dev/" + physicalDiskBaseByPattern(base)
}

// physicalDiskNameFromSysfs 通过 sysfs 判断设备是整盘还是分区，信息不足时返回 false。
func physicalDiskNameFromSysfs(base string) (string, bool) {
	if _, err := os.Stat(filepath.Join(sysfsRoot, "block", base)); err == nil {
		return base, true // /sys/block 下直接存在 → 本身就是整盘
	}

	devPath, err := filepath.EvalSymlinks(filepath.Join(sysfsRoot, "class", "block", base))
	if err != nil {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(devPath, "partition")); err != nil {
		return "", false // 不是分区，又不在 /sys/block 下，交给命名规则兜底
	}

	parent := filepath.Base(filepath.Dir(devPath))
	if parent == "" || parent == "." || parent == string(filepath.Separator) {
		return "", false
	}
	return parent, true
}

// physicalDiskBaseByPattern 按命名规则推断整盘名，是 sysfs 不可用时的兜底。
func physicalDiskBaseByPattern(base string) string {
	if m := pStyleDiskPattern.FindStringSubmatch(base); m != nil {
		return m[1]
	}

	// sd/vd/hd/xvd 等：盘名不以数字结尾，去掉末尾数字即为整盘
	i := len(base)
	for i > 0 && base[i-1] >= '0' && base[i-1] <= '9' {
		i--
	}
	if stripped := base[:i]; stripped != "" {
		return stripped
	}
	return base // 解析失败则原样保留
}

// readPhysicalDiskSizeGB 从 /sys/block/<disk>/size 读取物理磁盘总容量（GB）。
// 读取失败返回 0，不影响其他展示逻辑。
func readPhysicalDiskSizeGB(physDev string) float64 {
	diskName := filepath.Base(physDev)
	data, err := os.ReadFile(filepath.Join(sysfsRoot, "block", diskName, "size"))
	if err != nil {
		return 0
	}
	sectors, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || sectors <= 0 {
		return 0
	}
	return float64(sectors) * bytesPerSector / bytesPerGB
}

// groupByPhysicalDisk 将挂载点列表按物理磁盘分组，每组内按挂载点排序。
// 返回结果按物理盘名升序排列。
func groupByPhysicalDisk(usages []DiskUsage) []PhysicalDisk {
	indexMap := map[string]int{} // physDev → index in result
	var result []PhysicalDisk

	for _, u := range usages {
		physDev := physicalDiskName(u.Device)
		idx, exists := indexMap[physDev]
		if !exists {
			idx = len(result)
			indexMap[physDev] = idx
			result = append(result, PhysicalDisk{
				Name:    physDev,
				TotalGB: readPhysicalDiskSizeGB(physDev),
			})
		}
		result[idx].Partitions = append(result[idx].Partitions, u)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func renderPhysicalDisks(w io.Writer, disks []PhysicalDisk) error {
	warnPercent := config().DiskWarnPercent

	for i, disk := range disks {
		// 物理盘标题行
		if disk.TotalGB > 0 {
			fmt.Fprintf(w, "● %s  [%s 物理容量]\n", disk.Name, formatCapacityByGB(disk.TotalGB))
		} else {
			fmt.Fprintf(w, "● %s\n", disk.Name)
		}

		// 分区明细表，缩进两格
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, p := range disk.Partitions {
			warn := ""
			if p.UsedPercent > warnPercent {
				warn = " [!]"
			}
			fmt.Fprintf(tw, "  %s\t%s\t总 %s\t已用 %s\t剩 %s\t%.1f%%%s\n",
				p.Device,
				p.MountPoint,
				formatCapacityByGB(p.TotalGB),
				formatCapacityByGB(p.UsedGB),
				formatCapacityByGB(p.FreeGB),
				p.UsedPercent,
				warn,
			)
		}
		if err := tw.Flush(); err != nil {
			return err
		}

		if i < len(disks)-1 {
			fmt.Fprintln(w)
		}
	}
	return nil
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

		disks := groupByPhysicalDisk(usages)
		if err := renderPhysicalDisks(os.Stdout, disks); err != nil {
			fmt.Fprintf(os.Stderr, "输出磁盘占用失败: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(diskCmd)
}
