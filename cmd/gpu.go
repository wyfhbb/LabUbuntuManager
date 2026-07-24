package cmd

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

const (
	nvidiaSMIBin = "nvidia-smi"

	// gpuQueryTimeout 限制单次 nvidia-smi 的执行时间。
	// MOTD 每次登录都会渲染，驱动挂起时 nvidia-smi 会卡在内核态不返回，
	// 没有超时就会把所有人的登录一起拖住。
	gpuQueryTimeout = 2 * time.Second

	// nvidiaPCIVendorID 是 NVIDIA 的 PCI 厂商号，用于在没有 nvidia-smi 时
	// 判断机器上到底插没插 N 卡 —— 这决定了「没装驱动」和「本来就没卡」的区分。
	nvidiaPCIVendorID = "0x10de"

	// gpuValueUnavailable 表示该项数值不可用（nvidia-smi 输出 [N/A] / [Not Supported]）。
	// 虚拟化环境和部分消费级卡查不到功耗，用负值区别于真实的 0。
	gpuValueUnavailable = -1

	// clockTicksPerSecond 是 /proc/<pid>/stat 里 starttime 的单位（USER_HZ）。
	// Linux 上固定为 100。
	clockTicksPerSecond = 100
)

// gpuStatusQueryFields 是 --query-gpu 的字段顺序，必须与 parseGPUStatusCSV 一致。
// name 放最后：它是唯一可能含逗号的字段，放末尾就能用 SplitN 保住剩余内容。
const gpuStatusQueryFields = "index,uuid,memory.total,memory.used,memory.free," +
	"utilization.gpu,temperature.gpu,power.draw,power.limit,driver_version,name"

const gpuStatusFieldCount = 11

// gpuProcessQueryFields 是 --query-compute-apps 的字段顺序。
// 这里拿不到卡号，只有 uuid，需要再用 ListGPUs 的结果映射回 index。
const gpuProcessQueryFields = "gpu_uuid,pid,used_gpu_memory,process_name"

const gpuProcessFieldCount = 4

// procRoot 指向 procfs 挂载点，测试时替换为夹具目录。
var procRoot = "/proc"

// GPUInfo 表示单张显卡的实时状态。
// 数值字段为 gpuValueUnavailable 表示该卡不支持该项查询。
type GPUInfo struct {
	Index         int
	UUID          string
	Name          string
	MemoryTotalMB float64
	MemoryUsedMB  float64
	MemoryFreeMB  float64
	UtilPercent   float64
	TempC         float64
	PowerW        float64
	PowerLimitW   float64
	DriverVersion string
}

// GPUProcess 表示一个占用显存的计算进程。
// User / Elapsed 不来自 nvidia-smi，由 /proc/<pid> 补齐。
type GPUProcess struct {
	GPUUUID  string
	GPUIndex int
	PID      int
	MemoryMB float64
	Command  string
	User     string
	Elapsed  time.Duration
}

// GPUProvider 定义 GPU 状态查询接口，与 DiskUsageProvider 同构。
type GPUProvider interface {
	ListGPUs() ([]GPUInfo, error)
	ListGPUProcesses() ([]GPUProcess, error)
}

// ── 故障分类 ──────────────────────────────────────────────────────────────────

// gpuFaultKind 是 nvidia-smi 无法给出结果时的原因分类。
type gpuFaultKind int

const (
	// gpuNoNvidiaHardware 表示机器上根本没有 N 卡：属于正常降级，不是故障。
	gpuNoNvidiaHardware gpuFaultKind = iota
	gpuDriverNotInstalled
	gpuDriverMismatch
	gpuDriverNotLoaded
	gpuNoDeviceFound
	gpuNoPermission
	gpuNVMLUnknown
	gpuQueryTimedOut
	gpuFaultUnknown
)

// GPUFault 描述一次 GPU 查询失败：结论、成因、原始现象、处理步骤。
//
// 存在的意义是把 "exit status 255" 这类无信息量的报错，翻译成管理员
// 照着就能修的东西 —— 其中最常见的就是驱动升级后没重启导致的
// "Driver/library version mismatch"。
type GPUFault struct {
	Kind  gpuFaultKind
	Title string   // 一句话结论
	Cause string   // 为什么会这样
	Raw   string   // nvidia-smi 的原始输出，便于排查未覆盖的情况
	Fixes []string // 处理步骤，按推荐顺序
}

func (f *GPUFault) Error() string { return f.Title }

// isHardwareAbsent 表示"本机没有 N 卡"，调用方据此静默跳过而非报错。
func (f *GPUFault) isHardwareAbsent() bool { return f.Kind == gpuNoNvidiaHardware }

// asGPUFault 从 error 里取出 *GPUFault，取不到时兜底成 gpuFaultUnknown。
func asGPUFault(err error) *GPUFault {
	var fault *GPUFault
	if errors.As(err, &fault) {
		return fault
	}
	return &GPUFault{
		Kind:  gpuFaultUnknown,
		Title: "GPU 状态查询失败",
		Raw:   err.Error(),
		Fixes: []string{"手动执行看完整输出: nvidia-smi"},
	}
}

// classifyGPUFailure 把 nvidia-smi 的失败输出归类成具体故障。
//
// 纯函数：输入是 nvidia-smi 的 stdout+stderr 原文和是否超时，
// 不同版本把错误写在哪一路并不一致，所以两路一起传进来匹配。
func classifyGPUFailure(output string, timedOut bool) *GPUFault {
	raw := strings.TrimSpace(output)
	lower := strings.ToLower(raw)

	if timedOut {
		return &GPUFault{
			Kind:  gpuQueryTimedOut,
			Title: fmt.Sprintf("nvidia-smi 超过 %s 无响应，GPU 可能已挂起", gpuQueryTimeout),
			Cause: "nvidia-smi 卡在内核态通常意味着 GPU hang 或驱动死锁，此时跑在卡上的任务多半也已经卡死。",
			Raw:   raw,
			Fixes: []string{
				"查看内核有没有报 Xid: sudo dmesg | grep -i xid",
				"确认还有哪些进程占着卡: sudo fuser -v /dev/nvidia*",
				"确认无人使用后重启机器: sudo reboot",
			},
		}
	}

	switch {
	case strings.Contains(lower, "driver/library version mismatch"):
		return &GPUFault{
			Kind:  gpuDriverMismatch,
			Title: "NVIDIA 驱动与已加载的内核模块版本不一致，GPU 当前不可用",
			Cause: "系统（多半是 apt 升级）更新了 NVIDIA 驱动的用户态库，但内核里跑的还是旧版 nvidia 模块。" +
				"内核模块只能在重启或卸载后才会换成新版，在此之前所有 CUDA 程序都会失败。",
			Raw: raw,
			Fixes: []string{
				"重启机器（最可靠）: sudo reboot",
				"对比两边版本: cat /proc/driver/nvidia/version   # 已加载的内核模块",
				"              dpkg -l | grep nvidia-driver      # 已安装的驱动包",
				"不便重启时可热重载，先确认没人在用 GPU: sudo fuser -v /dev/nvidia*",
				"再卸载并重新加载模块: sudo rmmod nvidia_uvm nvidia_drm nvidia_modeset nvidia && sudo modprobe nvidia",
			},
		}

	case strings.Contains(lower, "couldn't communicate with the nvidia driver"),
		strings.Contains(lower, "nvidia driver is not loaded"):
		return &GPUFault{
			Kind:  gpuDriverNotLoaded,
			Title: "NVIDIA 内核模块未加载，GPU 当前不可用",
			Cause: "nvidia-smi 装了，但内核里没有可用的 nvidia 模块。常见原因：内核升级后驱动没重新编译" +
				"（DKMS 没跑成功）、Secure Boot 拒绝加载未签名模块、或驱动安装本身失败。",
			Raw: raw,
			Fixes: []string{
				"确认模块是否加载: lsmod | grep nvidia",
				"手动加载并看报错: sudo modprobe nvidia",
				"内核升级后驱动要重编: dkms status   # 应能看到 nvidia 对当前内核是 installed",
				"当前内核版本: uname -r",
				"Secure Boot 会拒绝未签名模块: mokutil --sb-state   # enabled 时需签名或关闭",
			},
		}

	case strings.Contains(lower, "no devices were found"),
		strings.Contains(lower, "no devices found"):
		return newNoDeviceFault(raw)

	case strings.Contains(lower, "insufficient permissions"):
		return &GPUFault{
			Kind:  gpuNoPermission,
			Title: "当前用户没有访问 GPU 的权限",
			Cause: "NVML 拒绝了本次访问，通常是 /dev/nvidia* 设备节点权限被改过。",
			Raw:   raw,
			Fixes: []string{
				"查看设备节点权限: ls -l /dev/nvidia*",
				"用 sudo 再试一次确认是否确为权限问题: sudo nvidia-smi",
			},
		}

	case strings.Contains(lower, "failed to initialize nvml"):
		// 上面几条更具体的都没命中，剩下的多是容器内 cgroup 变动导致的 Unknown Error
		return &GPUFault{
			Kind:  gpuNVMLUnknown,
			Title: "NVML 初始化失败，GPU 当前不可用",
			Cause: "常见于容器内 cgroup 变动后设备节点失效，或驱动处于异常状态。",
			Raw:   raw,
			Fixes: []string{
				"容器场景：先在宿主机上执行 nvidia-smi 确认宿主是否正常",
				"宿主正常则重启容器，让设备节点重新挂进去",
				"宿主也异常时按驱动问题排查: cat /proc/driver/nvidia/version",
			},
		}
	}

	return &GPUFault{
		Kind:  gpuFaultUnknown,
		Title: "nvidia-smi 执行失败",
		Cause: "不是已知的常见故障，需要看原始输出判断。",
		Raw:   raw,
		Fixes: []string{"手动执行看完整输出: nvidia-smi"},
	}
}

// newNoDeviceFault 描述"驱动能通信但一张卡都认不到"的情况。
func newNoDeviceFault(raw string) *GPUFault {
	return &GPUFault{
		Kind:  gpuNoDeviceFound,
		Title: "驱动正常，但没有识别到任何 NVIDIA 显卡（疑似掉卡）",
		Cause: "驱动能正常通信却报告 0 张卡。通常是显卡从 PCIe 上掉了（供电、插槽接触、过热保护），" +
			"或者卡被直通给了虚拟机/容器。",
		Raw: raw,
		Fixes: []string{
			"确认 PCIe 上还认不认得到卡: lspci | grep -i nvidia",
			"查看内核有没有报错: sudo dmesg | grep -i -E 'nvidia|xid|pcie'",
			"物理排查: 供电线、PCIe 插槽接触、机箱温度",
		},
	}
}

// nvidiaSMIMissingFault 处理找不到 nvidia-smi 的情况。
// 有没有 N 卡决定了这是"该装驱动"还是"本机就没卡"，两者的处置完全不同。
func nvidiaSMIMissingFault(hasHardware bool) *GPUFault {
	if !hasHardware {
		return &GPUFault{
			Kind:  gpuNoNvidiaHardware,
			Title: "本机没有 NVIDIA 显卡",
		}
	}
	return &GPUFault{
		Kind:  gpuDriverNotInstalled,
		Title: "检测到 NVIDIA 显卡，但没有安装驱动（找不到 nvidia-smi）",
		Cause: "PCIe 上能看到 NVIDIA 显示设备，系统里却没有 nvidia-smi，说明驱动没装或没装成功。",
		Fixes: []string{
			"查看可选驱动: ubuntu-drivers devices",
			"安装推荐驱动: sudo ubuntu-drivers autoinstall",
			"安装完成后重启: sudo reboot",
		},
	}
}

// hasNvidiaPCIDevice 扫描 sysfs 判断机器上是否插着 NVIDIA 显卡。
// 不依赖 lspci —— 最小化安装的服务器上不一定装了 pciutils。
func hasNvidiaPCIDevice() bool {
	devicesDir := filepath.Join(sysfsRoot, "bus", "pci", "devices")
	entries, err := os.ReadDir(devicesDir)
	if err != nil {
		return false
	}

	for _, entry := range entries {
		dir := filepath.Join(devicesDir, entry.Name())

		vendor, err := os.ReadFile(filepath.Join(dir, "vendor"))
		if err != nil || strings.TrimSpace(string(vendor)) != nvidiaPCIVendorID {
			continue
		}
		// class 0x03xxxx 是显示控制器，借此排除同厂的 HDMI 音频、USB-C 桥接等设备
		class, err := os.ReadFile(filepath.Join(dir, "class"))
		if err != nil {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(string(class)), "0x03") {
			return true
		}
	}
	return false
}

// ── nvidia-smi provider ──────────────────────────────────────────────────────

// NvidiaSMIProvider 通过 nvidia-smi 提供 GPU 查询能力。
// 所有失败都归一成 *GPUFault，调用方据此给出诊断而不是抛一句 exit status。
type NvidiaSMIProvider struct {
	timeout time.Duration
}

func NewNvidiaSMIProvider() *NvidiaSMIProvider {
	return &NvidiaSMIProvider{timeout: gpuQueryTimeout}
}

// run 执行一次 nvidia-smi，失败时返回 *GPUFault。
func (p *NvidiaSMIProvider) run(args ...string) (string, error) {
	if _, err := exec.LookPath(nvidiaSMIBin); err != nil {
		return "", nvidiaSMIMissingFault(hasNvidiaPCIDevice())
	}

	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	c := exec.CommandContext(ctx, nvidiaSMIBin, args...)
	c.Stdout = &stdout
	c.Stderr = &stderr
	// LANG=C：故障分类靠匹配英文原文，不能让 locale 把错误信息换掉
	c.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")

	if err := c.Run(); err != nil {
		timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
		return "", classifyGPUFailure(stdout.String()+"\n"+stderr.String(), timedOut)
	}
	return stdout.String(), nil
}

func (p *NvidiaSMIProvider) ListGPUs() ([]GPUInfo, error) {
	out, err := p.run("--query-gpu="+gpuStatusQueryFields, "--format=csv,noheader,nounits")
	if err != nil {
		return nil, err
	}

	gpus, err := parseGPUStatusCSV(out)
	if err != nil {
		return nil, err
	}
	if len(gpus) == 0 {
		// 老版本 nvidia-smi 在没有卡时不报错，只是输出空 —— 归一到同一个故障上。
		// 连 PCI 设备都没有，说明本机就没卡，不算故障。
		if !hasNvidiaPCIDevice() {
			return nil, &GPUFault{Kind: gpuNoNvidiaHardware, Title: "本机没有 NVIDIA 显卡"}
		}
		return nil, newNoDeviceFault("nvidia-smi 未返回任何显卡")
	}
	return gpus, nil
}

func (p *NvidiaSMIProvider) ListGPUProcesses() ([]GPUProcess, error) {
	gpus, err := p.ListGPUs()
	if err != nil {
		return nil, err
	}

	out, err := p.run("--query-compute-apps="+gpuProcessQueryFields, "--format=csv,noheader,nounits")
	if err != nil {
		return nil, err
	}

	procs, err := parseGPUProcessCSV(out)
	if err != nil {
		return nil, err
	}

	indexByUUID := make(map[string]int, len(gpus))
	for _, g := range gpus {
		indexByUUID[g.UUID] = g.Index
	}

	uptime := systemUptime()
	for i := range procs {
		if idx, ok := indexByUUID[procs[i].GPUUUID]; ok {
			procs[i].GPUIndex = idx
		} else {
			procs[i].GPUIndex = -1
		}
		enrichGPUProcessFromProc(&procs[i], uptime)
	}
	return procs, nil
}

// CUDAVersion 返回驱动支持的最高 CUDA 版本，取不到时返回空串。
//
// --query-gpu 没有这一项，只能从 nvidia-smi -q 的 "键 : 值" 输出里取。
// 只在 gpu status 里调用，不进 MOTD 渲染路径（-q 会 dump 全部信息，偏慢）。
func (p *NvidiaSMIProvider) CUDAVersion() string {
	out, err := p.run("-q")
	if err != nil {
		return ""
	}
	return parseNvidiaSMIQueryField(out, "CUDA Version")
}

// ── 解析（纯函数）─────────────────────────────────────────────────────────────

// parseGPUStatusCSV 解析 --query-gpu 的 CSV 输出，字段顺序见 gpuStatusQueryFields。
func parseGPUStatusCSV(out string) ([]GPUInfo, error) {
	var gpus []GPUInfo

	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		fields := splitCSVFields(line, gpuStatusFieldCount)
		if len(fields) < gpuStatusFieldCount {
			return nil, fmt.Errorf("nvidia-smi 输出字段数不足（期望 %d，实际 %d）: %s",
				gpuStatusFieldCount, len(fields), line)
		}

		index, err := strconv.Atoi(fields[0])
		if err != nil {
			return nil, fmt.Errorf("无法解析卡号 %q: %w", fields[0], err)
		}

		gpus = append(gpus, GPUInfo{
			Index:         index,
			UUID:          fields[1],
			MemoryTotalMB: parseGPUValue(fields[2]),
			MemoryUsedMB:  parseGPUValue(fields[3]),
			MemoryFreeMB:  parseGPUValue(fields[4]),
			UtilPercent:   parseGPUValue(fields[5]),
			TempC:         parseGPUValue(fields[6]),
			PowerW:        parseGPUValue(fields[7]),
			PowerLimitW:   parseGPUValue(fields[8]),
			DriverVersion: fields[9],
			Name:          fields[10],
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取 nvidia-smi 输出失败: %w", err)
	}
	return gpus, nil
}

// parseGPUProcessCSV 解析 --query-compute-apps 的 CSV 输出。
// 无进程时 nvidia-smi 输出为空，返回空切片而非错误。
func parseGPUProcessCSV(out string) ([]GPUProcess, error) {
	var procs []GPUProcess

	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		fields := splitCSVFields(line, gpuProcessFieldCount)
		if len(fields) < gpuProcessFieldCount {
			return nil, fmt.Errorf("nvidia-smi 进程输出字段数不足（期望 %d，实际 %d）: %s",
				gpuProcessFieldCount, len(fields), line)
		}

		pid, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("无法解析 PID %q: %w", fields[1], err)
		}

		procs = append(procs, GPUProcess{
			GPUUUID:  fields[0],
			PID:      pid,
			MemoryMB: parseGPUValue(fields[2]),
			Command:  fields[3],
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取 nvidia-smi 进程输出失败: %w", err)
	}
	return procs, nil
}

// splitCSVFields 按逗号切分并去掉两侧空白，最多切成 n 段。
// 限制段数是为了让最后一个字段（显卡名 / 进程命令行）能安全地包含逗号。
func splitCSVFields(line string, n int) []string {
	parts := strings.SplitN(line, ",", n)
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// parseGPUValue 解析 nvidia-smi 的数值字段。
// 部分型号或虚拟化环境会输出 [N/A] / [Not Supported]，此时返回 gpuValueUnavailable。
func parseGPUValue(raw string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return gpuValueUnavailable
	}
	return value
}

// parseNvidiaSMIQueryField 从 nvidia-smi -q 的 "键 : 值" 输出里取指定键的值。
func parseNvidiaSMIQueryField(out, key string) string {
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		k, v, ok := strings.Cut(scanner.Text(), ":")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		if value := strings.TrimSpace(v); value != "" && value != "N/A" {
			return value
		}
	}
	return ""
}

// ── /proc 补齐进程归属与运行时长 ───────────────────────────────────────────────

// enrichGPUProcessFromProc 用 /proc/<pid> 补上进程属主、完整命令行与已运行时长。
// 进程可能在两次查询之间退出，读不到就保留 nvidia-smi 给的信息。
func enrichGPUProcessFromProc(proc *GPUProcess, uptime time.Duration) {
	dir := filepath.Join(procRoot, strconv.Itoa(proc.PID))

	if data, err := os.ReadFile(filepath.Join(dir, "status")); err == nil {
		if uid, ok := parseProcStatusUID(string(data)); ok {
			proc.User = lookupUsername(uid)
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
		if cmdline := parseProcCmdline(string(data)); cmdline != "" {
			proc.Command = cmdline
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, "stat")); err == nil {
		if ticks, ok := parseProcStartTicks(string(data)); ok {
			proc.Elapsed = elapsedSinceStart(ticks, uptime)
		}
	}
}

// parseProcStatusUID 从 /proc/<pid>/status 内容里取 real UID。
// 格式为 "Uid:\t1000\t1000\t1000\t1000"（real / effective / saved / fs）。
func parseProcStatusUID(content string) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		rest, ok := strings.CutPrefix(scanner.Text(), "Uid:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return "", false
		}
		return fields[0], true
	}
	return "", false
}

// parseProcCmdline 把 /proc/<pid>/cmdline 的 NUL 分隔参数拼成一行。
func parseProcCmdline(raw string) string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == '\x00' })
	return strings.TrimSpace(strings.Join(parts, " "))
}

// parseProcStartTicks 从 /proc/<pid>/stat 取 starttime（自开机以来的时钟节拍）。
//
// starttime 是第 22 个字段，但第 2 个字段是括号包起来的进程名、可能含空格和右括号，
// 所以从最后一个 ')' 之后再按空白切分。切分后 fields[0] 是第 3 个字段（state），
// 因此第 22 个字段的下标是 19。
func parseProcStartTicks(stat string) (int64, bool) {
	idx := strings.LastIndex(stat, ")")
	if idx < 0 {
		return 0, false
	}

	const starttimeOffset = 19
	fields := strings.Fields(stat[idx+1:])
	if len(fields) <= starttimeOffset {
		return 0, false
	}

	ticks, err := strconv.ParseInt(fields[starttimeOffset], 10, 64)
	if err != nil || ticks < 0 {
		return 0, false
	}
	return ticks, true
}

// elapsedSinceStart 由进程启动节拍和系统运行时长算出进程已运行时长。
func elapsedSinceStart(startTicks int64, uptime time.Duration) time.Duration {
	started := time.Duration(startTicks) * time.Second / clockTicksPerSecond
	if uptime <= started {
		return 0
	}
	return uptime - started
}

// systemUptime 读取 /proc/uptime，失败时返回 0（届时运行时长显示为"未知"）。
func systemUptime() time.Duration {
	data, err := os.ReadFile(filepath.Join(procRoot, "uptime"))
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return time.Duration(secs * float64(time.Second))
}

// lookupUsername 把 UID 翻成用户名，查不到时退回 "uid:<n>"。
func lookupUsername(uid string) string {
	if u, err := osuser.LookupId(uid); err == nil {
		return u.Username
	}
	return "uid:" + uid
}

// ── 展示辅助 ─────────────────────────────────────────────────────────────────

// formatGPUMemory 把 MB 显存格式化成人类可读容量。
func formatGPUMemory(mb float64) string {
	if mb < 0 {
		return "N/A"
	}
	return formatCapacityByGB(mb / mbPerGB)
}

// formatGPUValue 格式化带单位的数值，不可用时显示 N/A。
func formatGPUValue(value float64, format, unit string) string {
	if value < 0 {
		return "N/A"
	}
	return fmt.Sprintf(format, value) + unit
}

// formatGPUDuration 把时长格式化成 "2 天 3 小时" / "5 小时 12 分" / "37 分"。
func formatGPUDuration(d time.Duration) string {
	if d <= 0 {
		return "未知"
	}
	totalMinutes := int(d.Minutes())
	days := totalMinutes / (24 * 60)
	hours := (totalMinutes % (24 * 60)) / 60
	minutes := totalMinutes % 60

	switch {
	case days > 0:
		return fmt.Sprintf("%d 天 %d 小时", days, hours)
	case hours > 0:
		return fmt.Sprintf("%d 小时 %d 分", hours, minutes)
	default:
		return fmt.Sprintf("%d 分", minutes)
	}
}

// truncateRunes 按字符数截断，超长时以 … 结尾。
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

// renderGPUFault 打印故障诊断：结论 / 现象 / 成因 / 处理步骤。
func renderGPUFault(w io.Writer, fault *GPUFault) {
	fmt.Fprintf(w, "%s⚠ %s%s\n", colorRed+colorBold, fault.Title, colorReset)

	if fault.Raw != "" {
		fmt.Fprintln(w, "\n  现象:")
		for _, line := range strings.Split(strings.TrimSpace(fault.Raw), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				fmt.Fprintf(w, "    %s%s%s\n", colorDim, line, colorReset)
			}
		}
	}
	if fault.Cause != "" {
		fmt.Fprintf(w, "\n  成因:\n    %s\n", fault.Cause)
	}
	if len(fault.Fixes) > 0 {
		fmt.Fprintln(w, "\n  处理:")
		for _, fix := range fault.Fixes {
			fmt.Fprintf(w, "    %s\n", fix)
		}
	}
}

// ── gpu status ───────────────────────────────────────────────────────────────

var gpuCmd = &cobra.Command{
	Use:   "gpu",
	Short: "查看 GPU 状态与占用情况（所有用户可用）",
	Long: `查看 NVIDIA 显卡的实时状态和占用情况。

子命令：
  status   每张卡的显存 / 利用率 / 温度 / 功耗，以及驱动与 CUDA 版本
  top      GPU 计算进程按用户聚合，看清谁占着哪张卡

无 NVIDIA 显卡或驱动异常时会给出明确诊断，两个命令都不需要 root。`,
}

var gpuStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看每张显卡的显存 / 利用率 / 温度 / 功耗",
	Run: func(cmd *cobra.Command, args []string) {
		provider := NewNvidiaSMIProvider()

		gpus, err := provider.ListGPUs()
		if err != nil {
			fault := asGPUFault(err)
			if fault.isHardwareAbsent() {
				fmt.Println(fault.Title)
				return
			}
			renderGPUFault(os.Stdout, fault)
			os.Exit(1)
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "卡号\t名称\t显存(已用/总量)\t利用率\t温度\t功耗")
		fmt.Fprintln(tw, "----\t----\t---------------\t------\t----\t----")
		for _, g := range gpus {
			fmt.Fprintf(tw, "%d\t%s\t%s / %s\t%s\t%s\t%s / %s\n",
				g.Index,
				g.Name,
				formatGPUMemory(g.MemoryUsedMB),
				formatGPUMemory(g.MemoryTotalMB),
				formatGPUValue(g.UtilPercent, "%.0f", "%"),
				formatGPUValue(g.TempC, "%.0f", "°C"),
				formatGPUValue(g.PowerW, "%.0f", " W"),
				formatGPUValue(g.PowerLimitW, "%.0f", " W"),
			)
		}
		tw.Flush()

		fmt.Println()
		fmt.Printf("驱动版本:  %s\n", gpus[0].DriverVersion)
		if cuda := provider.CUDAVersion(); cuda != "" {
			fmt.Printf("CUDA 版本: %s（驱动支持的最高版本）\n", cuda)
		}
		fmt.Println()
		fmt.Println("查看谁在占用: server-mgr gpu top")
	},
}

// ── gpu top ──────────────────────────────────────────────────────────────────

// gpuUserUsage 汇总单个用户在所有卡上的 GPU 占用。
type gpuUserUsage struct {
	username   string
	totalMB    float64
	procCount  int
	gpuIndexes []int
}

var gpuTopCmd = &cobra.Command{
	Use:   "top",
	Short: "按用户查看 GPU 进程占用（谁占着哪张卡）",
	Run: func(cmd *cobra.Command, args []string) {
		provider := NewNvidiaSMIProvider()

		procs, err := provider.ListGPUProcesses()
		if err != nil {
			fault := asGPUFault(err)
			if fault.isHardwareAbsent() {
				fmt.Println(fault.Title)
				return
			}
			renderGPUFault(os.Stdout, fault)
			os.Exit(1)
		}

		if len(procs) == 0 {
			fmt.Println("当前没有进程占用 GPU")
			return
		}

		fmt.Println("━━ 按用户汇总 ━━")
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "用户\t进程数\t显存合计\t占用卡号")
		fmt.Fprintln(tw, "----\t------\t--------\t--------")
		for _, u := range aggregateGPUUsageByUser(procs) {
			fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n",
				u.username, u.procCount, formatGPUMemory(u.totalMB), formatGPUIndexes(u.gpuIndexes))
		}
		tw.Flush()

		fmt.Println()
		fmt.Println("━━ 进程明细 ━━")
		tw = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "用户\t卡号\t显存\t已运行\tPID\t命令")
		fmt.Fprintln(tw, "----\t----\t----\t------\t---\t----")
		for _, p := range sortGPUProcesses(procs) {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n",
				displayGPUUser(p.User),
				formatGPUIndex(p.GPUIndex),
				formatGPUMemory(p.MemoryMB),
				formatGPUDuration(p.Elapsed),
				p.PID,
				truncateRunes(p.Command, 60),
			)
		}
		tw.Flush()
	},
}

// aggregateGPUUsageByUser 把进程按用户聚合，按显存合计降序返回。
func aggregateGPUUsageByUser(procs []GPUProcess) []gpuUserUsage {
	byUser := map[string]*gpuUserUsage{}
	seenGPU := map[string]map[int]bool{}
	var order []string

	for _, p := range procs {
		name := displayGPUUser(p.User)
		usage, exists := byUser[name]
		if !exists {
			usage = &gpuUserUsage{username: name}
			byUser[name] = usage
			seenGPU[name] = map[int]bool{}
			order = append(order, name)
		}
		usage.procCount++
		if p.MemoryMB > 0 {
			usage.totalMB += p.MemoryMB
		}
		if !seenGPU[name][p.GPUIndex] {
			seenGPU[name][p.GPUIndex] = true
			usage.gpuIndexes = append(usage.gpuIndexes, p.GPUIndex)
		}
	}

	result := make([]gpuUserUsage, 0, len(order))
	for _, name := range order {
		sort.Ints(byUser[name].gpuIndexes)
		result = append(result, *byUser[name])
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].totalMB > result[j].totalMB })
	return result
}

// sortGPUProcesses 按 卡号 → 显存降序 排列进程明细。
func sortGPUProcesses(procs []GPUProcess) []GPUProcess {
	sorted := make([]GPUProcess, len(procs))
	copy(sorted, procs)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].GPUIndex != sorted[j].GPUIndex {
			return sorted[i].GPUIndex < sorted[j].GPUIndex
		}
		return sorted[i].MemoryMB > sorted[j].MemoryMB
	})
	return sorted
}

// displayGPUUser 处理进程已退出、读不到属主的情况。
func displayGPUUser(name string) string {
	if name == "" {
		return "未知"
	}
	return name
}

func formatGPUIndex(index int) string {
	if index < 0 {
		return "?"
	}
	return strconv.Itoa(index)
}

func formatGPUIndexes(indexes []int) string {
	parts := make([]string, 0, len(indexes))
	for _, i := range indexes {
		parts = append(parts, formatGPUIndex(i))
	}
	return strings.Join(parts, ",")
}

// ── MOTD 段落 ────────────────────────────────────────────────────────────────

// renderMotdGPUs 输出 MOTD 的 GPU 概览段落。
//
// 无 N 卡的机器整段跳过；驱动异常时只给一行结论加排查入口，
// 完整的处理步骤留给 gpu status，不把一屏排障说明塞进登录信息。
func renderMotdGPUs() {
	gpus, err := NewNvidiaSMIProvider().ListGPUs()
	if err != nil {
		fault := asGPUFault(err)
		if fault.isHardwareAbsent() {
			return
		}
		fmt.Println()
		fmt.Printf("  %sGPU ⚠ %s%s\n", colorRed+colorBold, fault.Title, colorReset)
		fmt.Printf("  %s     详情与处理: server-mgr gpu status%s\n", colorDim, colorReset)
		return
	}

	fmt.Println()
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, g := range gpus {
		// 告警只加在最后一列：tabwriter 按字节数算宽度，
		// 把 ANSI 转义塞进中间列会把后面所有列顶歪。
		warn := ""
		if g.MemoryTotalMB > 0 && g.MemoryFreeMB >= 0 &&
			g.MemoryFreeMB/g.MemoryTotalMB < gpuLowMemoryRatio {
			warn = colorYellow + " [显存吃紧]" + colorReset
		}
		fmt.Fprintf(tw, "  GPU %d\t%s\t空闲 %s / %s\t%s\t%s%s\n",
			g.Index,
			truncateRunes(g.Name, 28),
			formatGPUMemory(g.MemoryFreeMB),
			formatGPUMemory(g.MemoryTotalMB),
			formatGPUValue(g.UtilPercent, "%.0f", "%"),
			formatGPUValue(g.TempC, "%.0f", "°C"),
			warn,
		)
	}
	tw.Flush()
}

// gpuLowMemoryRatio 是 MOTD 里把空闲显存标黄的阈值（剩余不足总量的 10%）。
const gpuLowMemoryRatio = 0.1

func init() {
	gpuCmd.AddCommand(gpuStatusCmd)
	gpuCmd.AddCommand(gpuTopCmd)

	rootCmd.AddCommand(gpuCmd)
}
