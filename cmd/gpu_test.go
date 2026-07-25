package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── CSV 解析 ─────────────────────────────────────────────────────────────────

func TestParseGPUStatusCSVSingleGPU(t *testing.T) {
	// 字段顺序见 gpuStatusQueryFields：
	// index,uuid,memory.total,memory.used,memory.free,utilization.gpu,
	// temperature.gpu,power.draw,power.limit,driver_version,name
	out := "0, GPU-abc123, 24564, 2100, 22464, 35, 52, 120.35, 450.00, 550.54.14, NVIDIA GeForce RTX 4090\n"

	gpus, err := parseGPUStatusCSV(out)
	if err != nil {
		t.Fatalf("parseGPUStatusCSV 返回错误: %v", err)
	}
	if len(gpus) != 1 {
		t.Fatalf("期望 1 张卡，实际 %d 张", len(gpus))
	}

	g := gpus[0]
	switch {
	case g.Index != 0:
		t.Errorf("卡号 = %d，期望 0", g.Index)
	case g.UUID != "GPU-abc123":
		t.Errorf("UUID = %q", g.UUID)
	case g.Name != "NVIDIA GeForce RTX 4090":
		t.Errorf("名称 = %q", g.Name)
	case g.MemoryTotalMB != 24564 || g.MemoryUsedMB != 2100 || g.MemoryFreeMB != 22464:
		t.Errorf("显存解析错误: %+v", g)
	case g.UtilPercent != 35 || g.TempC != 52:
		t.Errorf("利用率/温度解析错误: %+v", g)
	case g.PowerW != 120.35 || g.PowerLimitW != 450:
		t.Errorf("功耗解析错误: %+v", g)
	case g.DriverVersion != "550.54.14":
		t.Errorf("驱动版本 = %q", g.DriverVersion)
	}
}

func TestParseGPUStatusCSVMultipleGPUs(t *testing.T) {
	// 第二张卡查不到功耗（虚拟化环境常见），应解析成 gpuValueUnavailable 而非 0
	out := strings.Join([]string{
		"0, GPU-aaa, 81559, 40000, 41559, 98, 71, 320.10, 400.00, 535.183.01, NVIDIA A100-SXM4-80GB",
		"1, GPU-bbb, 81559, 0, 81559, 0, 33, [N/A], [Not Supported], 535.183.01, NVIDIA A100-SXM4-80GB",
		"",
	}, "\n")

	gpus, err := parseGPUStatusCSV(out)
	if err != nil {
		t.Fatalf("parseGPUStatusCSV 返回错误: %v", err)
	}
	if len(gpus) != 2 {
		t.Fatalf("期望 2 张卡，实际 %d 张", len(gpus))
	}
	if gpus[1].Index != 1 {
		t.Errorf("第二张卡卡号 = %d，期望 1", gpus[1].Index)
	}
	if gpus[1].PowerW != gpuValueUnavailable || gpus[1].PowerLimitW != gpuValueUnavailable {
		t.Errorf("[N/A] 应解析为不可用，实际 draw=%v limit=%v", gpus[1].PowerW, gpus[1].PowerLimitW)
	}
	if formatGPUValue(gpus[1].PowerW, "%.0f", " W") != "N/A" {
		t.Errorf("不可用的数值应显示为 N/A")
	}
}

// 显卡名里带逗号时不能把后面的内容丢掉（name 是最后一个字段，靠 SplitN 保住）。
func TestParseGPUStatusCSVKeepsCommaInName(t *testing.T) {
	out := "0, GPU-x, 8192, 1024, 7168, 10, 40, 50.00, 120.00, 470.256.02, Quadro RTX 4000, Mobile\n"

	gpus, err := parseGPUStatusCSV(out)
	if err != nil {
		t.Fatalf("parseGPUStatusCSV 返回错误: %v", err)
	}
	if gpus[0].Name != "Quadro RTX 4000, Mobile" {
		t.Errorf("名称 = %q，期望保留逗号后的内容", gpus[0].Name)
	}
}

func TestParseGPUStatusCSVRejectsShortLine(t *testing.T) {
	if _, err := parseGPUStatusCSV("0, GPU-x, 8192\n"); err == nil {
		t.Fatal("字段数不足时应返回错误")
	}
}

func TestParseGPUProcessCSV(t *testing.T) {
	out := strings.Join([]string{
		"GPU-aaa, 12345, 20480, python",
		"GPU-bbb, 12346, 4096, /usr/bin/python3 train.py --lr 0.1",
		"",
	}, "\n")

	procs, err := parseGPUProcessCSV(out)
	if err != nil {
		t.Fatalf("parseGPUProcessCSV 返回错误: %v", err)
	}
	if len(procs) != 2 {
		t.Fatalf("期望 2 个进程，实际 %d 个", len(procs))
	}
	if procs[0].GPUUUID != "GPU-aaa" || procs[0].PID != 12345 || procs[0].MemoryMB != 20480 {
		t.Errorf("第一个进程解析错误: %+v", procs[0])
	}
	if procs[1].Command != "/usr/bin/python3 train.py --lr 0.1" {
		t.Errorf("命令 = %q", procs[1].Command)
	}
}

// 没有进程时 nvidia-smi 输出为空，应返回空结果而不是报错。
func TestParseGPUProcessCSVNoProcesses(t *testing.T) {
	for _, out := range []string{"", "\n", "   \n\n"} {
		procs, err := parseGPUProcessCSV(out)
		if err != nil {
			t.Fatalf("空输出不应报错，得到: %v", err)
		}
		if len(procs) != 0 {
			t.Fatalf("空输出应返回 0 个进程，实际 %d 个", len(procs))
		}
	}
}

func TestParseNvidiaSMIQueryField(t *testing.T) {
	out := strings.Join([]string{
		"==============NVSMI LOG==============",
		"Driver Version                        : 550.54.14",
		"CUDA Version                          : 12.4",
		"Attached GPUs                         : 2",
	}, "\n")

	if got := parseNvidiaSMIQueryField(out, "CUDA Version"); got != "12.4" {
		t.Errorf("CUDA Version = %q，期望 12.4", got)
	}
	if got := parseNvidiaSMIQueryField(out, "不存在的键"); got != "" {
		t.Errorf("查不到的键应返回空串，得到 %q", got)
	}

	// 610 起旧键被标记弃用，值后面跟着一串说明；新键 CUDA UMD Version 是干净的。
	// 键名精确匹配，"CUDA Version" 不能匹配到 "CUDA UMD Version" 那行。
	newDriver := strings.Join([]string{
		"Driver Version                : 610.43.02 [Deprecated; will be removed in CUDA 14.0. Use KMD Version instead]",
		"CUDA Version                  : 13.3 [Deprecated; will be removed in CUDA 14.0. Use CUDA UMD Version instead]",
		"CUDA UMD Version              : 13.3",
	}, "\n")

	if got := parseNvidiaSMIQueryField(newDriver, "CUDA Version"); !strings.HasPrefix(got, "13.3 [Deprecated") {
		t.Errorf("旧键应原样取到含弃用说明的值，得到 %q", got)
	}
	if got := parseNvidiaSMIQueryField(newDriver, "CUDA UMD Version"); got != "13.3" {
		t.Errorf("新键 CUDA UMD Version = %q，期望 13.3", got)
	}
}

func TestTrimNvidiaSMINote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"13.3 [Deprecated; will be removed in CUDA 14.0. Use CUDA UMD Version instead]", "13.3"},
		{"610.43.02 [Deprecated; will be removed in CUDA 14.0. Use KMD Version instead]", "610.43.02"},
		{"12.4", "12.4"},
		{"", ""},
	}
	for _, c := range cases {
		if got := trimNvidiaSMINote(c.in); got != c.want {
			t.Errorf("trimNvidiaSMINote(%q)=%q，期望 %q", c.in, got, c.want)
		}
	}
}

// ── 故障分类 ─────────────────────────────────────────────────────────────────

func TestClassifyGPUFailure(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   gpuFaultKind
	}{
		{
			// 最经典的一种：apt 升级驱动后没重启
			name:   "驱动与内核模块版本不一致",
			output: "Failed to initialize NVML: Driver/library version mismatch\nNVML library version: 550.54",
			want:   gpuDriverMismatch,
		},
		{
			name: "内核模块未加载",
			output: "NVIDIA-SMI has failed because it couldn't communicate with the NVIDIA driver. " +
				"Make sure that the latest NVIDIA driver is installed and running.",
			want: gpuDriverNotLoaded,
		},
		{
			name:   "认不到卡",
			output: "No devices were found",
			want:   gpuNoDeviceFound,
		},
		{
			name:   "权限不足",
			output: "Failed to initialize NVML: Insufficient Permissions",
			want:   gpuNoPermission,
		},
		{
			name:   "NVML 未知错误",
			output: "Failed to initialize NVML: Unknown Error",
			want:   gpuNVMLUnknown,
		},
		{
			name:   "未覆盖的报错",
			output: "some brand new failure mode",
			want:   gpuFaultUnknown,
		},
	}

	for _, c := range cases {
		fault := classifyGPUFailure(c.output, false)
		if fault.Kind != c.want {
			t.Errorf("%s: Kind = %d，期望 %d", c.name, fault.Kind, c.want)
		}
		if fault.Title == "" {
			t.Errorf("%s: 每种故障都要有一句话结论", c.name)
		}
		if len(fault.Fixes) == 0 {
			t.Errorf("%s: 每种故障都要给出可执行的处理步骤", c.name)
		}
		if !strings.Contains(fault.Raw, strings.SplitN(c.output, "\n", 2)[0]) {
			t.Errorf("%s: 原始输出应保留下来便于排查，得到 %q", c.name, fault.Raw)
		}
	}
}

// 超时优先于输出内容判断：驱动挂起时 nvidia-smi 可能什么都没打印。
func TestClassifyGPUFailureTimeoutWins(t *testing.T) {
	fault := classifyGPUFailure("Failed to initialize NVML: Driver/library version mismatch", true)
	if fault.Kind != gpuQueryTimedOut {
		t.Errorf("超时应归为 gpuQueryTimedOut，得到 %d", fault.Kind)
	}
}

// 找不到 nvidia-smi 时，有没有 N 卡决定了是"该装驱动"还是"本机就没卡"。
func TestNvidiaSMIMissingFault(t *testing.T) {
	absent := nvidiaSMIMissingFault(false)
	if absent.Kind != gpuNoNvidiaHardware || !absent.isHardwareAbsent() {
		t.Errorf("无 N 卡时应归为 gpuNoNvidiaHardware，得到 %d", absent.Kind)
	}
	if len(absent.Fixes) != 0 {
		t.Error("本机没有 N 卡不是故障，不该给修复步骤")
	}

	missing := nvidiaSMIMissingFault(true)
	if missing.Kind != gpuDriverNotInstalled || missing.isHardwareAbsent() {
		t.Errorf("有 N 卡但没驱动时应归为 gpuDriverNotInstalled，得到 %d", missing.Kind)
	}
	if len(missing.Fixes) == 0 {
		t.Error("有 N 卡但没驱动时要给出安装步骤")
	}
}

// ── PCI 硬件探测 ─────────────────────────────────────────────────────────────

func TestHasNvidiaPCIDevice(t *testing.T) {
	cases := []struct {
		name   string
		vendor string
		class  string
		want   bool
	}{
		{"NVIDIA 显示控制器", "0x10de", "0x030000", true},
		{"NVIDIA 3D 控制器", "0x10de", "0x030200", true},
		{"NVIDIA 声卡（同厂但不是显卡）", "0x10de", "0x040300", false},
		{"其他厂商显卡", "0x1002", "0x030000", false},
	}

	for _, c := range cases {
		root := t.TempDir()
		useSysfsFixture(t, root)

		dir := filepath.Join(root, "bus", "pci", "devices", "0000:01:00.0")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "vendor"), []byte(c.vendor+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "class"), []byte(c.class+"\n"), 0644); err != nil {
			t.Fatal(err)
		}

		if got := hasNvidiaPCIDevice(); got != c.want {
			t.Errorf("%s: hasNvidiaPCIDevice() = %v，期望 %v", c.name, got, c.want)
		}
	}
}

func TestHasNvidiaPCIDeviceMissingSysfs(t *testing.T) {
	useSysfsFixture(t, t.TempDir())
	if hasNvidiaPCIDevice() {
		t.Error("sysfs 里没有 PCI 目录时应返回 false")
	}
}

// ── /proc 解析 ───────────────────────────────────────────────────────────────

func TestParseProcStatusUID(t *testing.T) {
	status := "Name:\tpython3\nState:\tS (sleeping)\nUid:\t1001\t1001\t1001\t1001\nGid:\t1001\t1001\t1001\t1001\n"

	uid, ok := parseProcStatusUID(status)
	if !ok || uid != "1001" {
		t.Errorf("parseProcStatusUID = (%q, %v)，期望 (\"1001\", true)", uid, ok)
	}

	if _, ok := parseProcStatusUID("Name:\tpython3\n"); ok {
		t.Error("没有 Uid 行时应返回 false")
	}
}

func TestParseProcCmdline(t *testing.T) {
	cases := map[string]string{
		"python3\x00train.py\x00--lr\x000.1\x00": "python3 train.py --lr 0.1",
		"nvidia-smi\x00":                         "nvidia-smi",
		"":                                       "",
	}
	for raw, want := range cases {
		if got := parseProcCmdline(raw); got != want {
			t.Errorf("parseProcCmdline(%q) = %q，期望 %q", raw, got, want)
		}
	}
}

// 进程名可能含空格和右括号，starttime 必须从最后一个 ')' 之后开始数。
func TestParseProcStartTicks(t *testing.T) {
	// 字段 3..22（state 起、starttime 止）共 20 个，starttime 取 987654
	tail := "S 1 1 1 0 -1 4194304 100 0 0 0 10 20 0 0 20 0 1 0 987654"
	cases := map[string]int64{
		"12345 (python3) " + tail:           987654,
		"12345 (weird (name) here) " + tail: 987654,
		"12345 (has space) " + tail:         987654,
	}
	for stat, want := range cases {
		got, ok := parseProcStartTicks(stat)
		if !ok || got != want {
			t.Errorf("parseProcStartTicks(%q) = (%d, %v)，期望 (%d, true)", stat, got, ok, want)
		}
	}

	if _, ok := parseProcStartTicks("12345 (python3) S 1 1"); ok {
		t.Error("字段不够时应返回 false")
	}
	if _, ok := parseProcStartTicks("没有右括号"); ok {
		t.Error("格式不对时应返回 false")
	}
}

func TestElapsedSinceStart(t *testing.T) {
	// 开机后 100 秒启动的进程，系统已运行 3700 秒 → 已运行 3600 秒
	uptime := 3700 * time.Second
	if got := elapsedSinceStart(100*clockTicksPerSecond, uptime); got != time.Hour {
		t.Errorf("elapsedSinceStart = %v，期望 1h", got)
	}
	// 启动时刻晚于 uptime（时钟异常）时归零，不能出现负数时长
	if got := elapsedSinceStart(9999*clockTicksPerSecond, uptime); got != 0 {
		t.Errorf("启动时刻晚于 uptime 时应返回 0，得到 %v", got)
	}
}

// ── 聚合与格式化 ─────────────────────────────────────────────────────────────

func TestAggregateGPUUsageByUser(t *testing.T) {
	procs := []GPUProcess{
		{User: "alice", GPUIndex: 1, MemoryMB: 20480, PID: 1},
		{User: "bob", GPUIndex: 2, MemoryMB: 4096, PID: 2},
		{User: "alice", GPUIndex: 0, MemoryMB: 10240, PID: 3},
		{User: "alice", GPUIndex: 0, MemoryMB: 1024, PID: 4},
		{User: "", GPUIndex: -1, MemoryMB: 512, PID: 5},
	}

	usages := aggregateGPUUsageByUser(procs)
	if len(usages) != 3 {
		t.Fatalf("期望 3 个用户，实际 %d 个: %+v", len(usages), usages)
	}

	// 按显存合计降序
	if usages[0].username != "alice" {
		t.Errorf("显存最多的应排第一，得到 %q", usages[0].username)
	}
	if usages[0].totalMB != 31744 || usages[0].procCount != 3 {
		t.Errorf("alice 汇总错误: %+v", usages[0])
	}
	// 同一张卡上的多个进程只算一次卡号，且按卡号升序
	if formatGPUIndexes(usages[0].gpuIndexes) != "0,1" {
		t.Errorf("占用卡号 = %q，期望 0,1", formatGPUIndexes(usages[0].gpuIndexes))
	}
	if usages[2].username != "未知" {
		t.Errorf("读不到属主的进程应归到「未知」，得到 %q", usages[2].username)
	}
}

func TestSortGPUProcesses(t *testing.T) {
	procs := []GPUProcess{
		{PID: 1, GPUIndex: 1, MemoryMB: 100},
		{PID: 2, GPUIndex: 0, MemoryMB: 100},
		{PID: 3, GPUIndex: 0, MemoryMB: 900},
	}

	got := sortGPUProcesses(procs)
	want := []int{3, 2, 1} // 卡号升序，同卡内显存降序
	for i, pid := range want {
		if got[i].PID != pid {
			t.Fatalf("排序结果 = %v，期望 PID 顺序 %v", got, want)
		}
	}
	if procs[0].PID != 1 {
		t.Error("不应改动传入的切片")
	}
}

func TestFormatGPUDuration(t *testing.T) {
	cases := map[time.Duration]string{
		0:                            "未知",
		-time.Second:                 "未知",
		37 * time.Minute:             "37 分",
		5*time.Hour + 12*time.Minute: "5 小时 12 分",
		51 * time.Hour:               "2 天 3 小时",
	}
	for d, want := range cases {
		if got := formatGPUDuration(d); got != want {
			t.Errorf("formatGPUDuration(%v) = %q，期望 %q", d, got, want)
		}
	}
}

func TestFormatGPUMemory(t *testing.T) {
	cases := map[float64]string{
		gpuValueUnavailable: "N/A",
		1024:                "1.00 GB",
		24564:               "23.99 GB",
	}
	for mb, want := range cases {
		if got := formatGPUMemory(mb); got != want {
			t.Errorf("formatGPUMemory(%v) = %q，期望 %q", mb, got, want)
		}
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("短", 10); got != "短" {
		t.Errorf("不超长时应原样返回，得到 %q", got)
	}
	// 按字符而非字节截断，中文不能被切坏
	if got := truncateRunes("显卡名称很长很长", 5); got != "显卡名称…" {
		t.Errorf("truncateRunes = %q，期望 显卡名称…", got)
	}
}
