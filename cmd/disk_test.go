package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDiskUsageFromMountsFiltersAndFillsCapacity(t *testing.T) {
	// 用临时目录当挂载点：statfs 对任意存在的路径都能取到所在文件系统的容量
	mountPoint := t.TempDir()

	mounts := strings.Join([]string{
		"/dev/sda1 " + mountPoint + " ext4 rw,relatime 0 0",
		"/dev/loop0 /snap/core/1234 squashfs ro,nodev 0 0",
		"tmpfs /run tmpfs rw,nosuid 0 0",
		"proc /proc proc rw,nosuid 0 0",
		"too few fields",
	}, "\n")

	usages, err := parseDiskUsageFromMounts(strings.NewReader(mounts))
	if err != nil {
		t.Fatalf("parseDiskUsageFromMounts 返回错误: %v", err)
	}
	if len(usages) != 1 {
		t.Fatalf("期望只保留 1 个挂载点，实际 %d 个: %+v", len(usages), usages)
	}

	u := usages[0]
	if u.Device != "/dev/sda1" || u.MountPoint != mountPoint {
		t.Fatalf("设备或挂载点解析错误: %+v", u)
	}
	if u.TotalGB <= 0 {
		t.Fatalf("总容量应大于 0，实际 %v", u.TotalGB)
	}
	if u.UsedPercent < 0 || u.UsedPercent > 100 {
		t.Fatalf("使用率应落在 0~100，实际 %v", u.UsedPercent)
	}
	if diff := u.TotalGB - (u.UsedGB + u.FreeGB); diff > 0.001 || diff < -0.001 {
		t.Fatalf("已用 + 剩余应等于总量: total=%v used=%v free=%v", u.TotalGB, u.UsedGB, u.FreeGB)
	}
}

func TestParseDiskUsageFromMountsErrorsOnMissingMountPoint(t *testing.T) {
	mounts := "/dev/sda1 /definitely/not/exist/server-mgr-test ext4 rw 0 0"

	if _, err := parseDiskUsageFromMounts(strings.NewReader(mounts)); err == nil {
		t.Fatal("挂载点不存在时应返回错误")
	}
}

// physicalDiskName 在 sysfs 查不到设备时应回落到命名规则。
// 用空的 sysfs 夹具确保走的确实是兜底分支。
func TestPhysicalDiskNameByPatternFallback(t *testing.T) {
	useSysfsFixture(t, t.TempDir())

	cases := map[string]string{
		"/dev/sda1":      "/dev/sda",
		"/dev/sdb2":      "/dev/sdb",
		"/dev/sda":       "/dev/sda",
		"/dev/vda1":      "/dev/vda",
		"/dev/nvme0n1p1": "/dev/nvme0n1",
		"/dev/mmcblk0p1": "/dev/mmcblk0",

		// 整盘直接挂载（未分区）：盘名本身以数字结尾，不能把末尾数字当分区号剥掉
		"/dev/nvme0n1": "/dev/nvme0n1",
		"/dev/nvme1n2": "/dev/nvme1n2",
		"/dev/mmcblk0": "/dev/mmcblk0",
		"/dev/md0":     "/dev/md0",
		"/dev/md0p1":   "/dev/md0",
		"/dev/dm-0":    "/dev/dm-0",
		"/dev/未知设备":    "/dev/未知设备",
	}

	for device, want := range cases {
		if got := physicalDiskName(device); got != want {
			t.Errorf("physicalDiskName(%q) = %q，期望 %q", device, got, want)
		}
	}
}

// sysfs 可用时以它为准：分区归到父盘，整盘（含未分区直接挂载的）归到自己。
func TestPhysicalDiskNameFromSysfs(t *testing.T) {
	root := t.TempDir()
	useSysfsFixture(t, root)

	// /sys/devices/pci/nvme0/nvme0n1{,p1}，并从 /sys/block、/sys/class/block 建链接
	diskDir := filepath.Join(root, "devices", "pci", "nvme0", "nvme0n1")
	partDir := filepath.Join(diskDir, "nvme0n1p1")
	if err := os.MkdirAll(partDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partDir, "partition"), []byte("1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(diskDir, "size"), []byte("1953525168\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(root, "block"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(diskDir, filepath.Join(root, "block", "nvme0n1")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "class", "block"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"nvme0n1": diskDir, "nvme0n1p1": partDir} {
		if err := os.Symlink(target, filepath.Join(root, "class", "block", name)); err != nil {
			t.Fatal(err)
		}
	}

	if got := physicalDiskName("/dev/nvme0n1p1"); got != "/dev/nvme0n1" {
		t.Errorf("分区应归到父盘，得到 %q", got)
	}
	if got := physicalDiskName("/dev/nvme0n1"); got != "/dev/nvme0n1" {
		t.Errorf("整盘应归到自己，得到 %q", got)
	}

	// 整盘容量要能读到：1953525168 扇区 × 512B ≈ 931.51 GB
	if got := readPhysicalDiskSizeGB("/dev/nvme0n1"); got < 931 || got > 932 {
		t.Errorf("readPhysicalDiskSizeGB = %v，期望约 931.5", got)
	}
	if got := readPhysicalDiskSizeGB("/dev/nvme0n1p1"); got != 0 {
		t.Errorf("分区没有 /sys/block 条目，应返回 0，得到 %v", got)
	}
}

// 整盘直接挂载时，分组结果应是"一块盘 + 它自己这个挂载点"，且带上物理容量。
func TestGroupByPhysicalDiskHandlesWholeDiskMount(t *testing.T) {
	root := t.TempDir()
	useSysfsFixture(t, root)

	diskDir := filepath.Join(root, "block", "nvme0n1")
	if err := os.MkdirAll(diskDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(diskDir, "size"), []byte("1953525168\n"), 0644); err != nil {
		t.Fatal(err)
	}

	disks := groupByPhysicalDisk([]DiskUsage{
		{Device: "/dev/nvme0n1", MountPoint: "/data", TotalGB: 900},
	})

	if len(disks) != 1 {
		t.Fatalf("期望 1 块物理盘，得到 %d 块: %+v", len(disks), disks)
	}
	if disks[0].Name != "/dev/nvme0n1" {
		t.Errorf("物理盘名 = %q，期望 /dev/nvme0n1", disks[0].Name)
	}
	if disks[0].TotalGB < 931 || disks[0].TotalGB > 932 {
		t.Errorf("物理容量 = %v，期望约 931.5（整盘挂载时也要能读到）", disks[0].TotalGB)
	}
	if len(disks[0].Partitions) != 1 || disks[0].Partitions[0].MountPoint != "/data" {
		t.Errorf("挂载点归属错误: %+v", disks[0].Partitions)
	}
}

// useSysfsFixture 把 sysfs 根目录指向夹具，测试结束后还原。
func useSysfsFixture(t *testing.T, root string) {
	t.Helper()
	original := sysfsRoot
	sysfsRoot = root
	t.Cleanup(func() { sysfsRoot = original })
}

func TestShouldIncludeMount(t *testing.T) {
	cases := []struct {
		name       string
		device     string
		mountPoint string
		fsType     string
		want       bool
	}{
		{"普通分区", "/dev/sda1", "/", "ext4", true},
		{"数据盘", "/dev/nvme0n1p1", "/workspace", "xfs", true},
		{"非块设备", "tmpfs", "/run", "tmpfs", false},
		{"loop 设备", "/dev/loop3", "/mnt/img", "ext4", false},
		{"squashfs", "/dev/sda1", "/mnt/img", "squashfs", false},
		{"snap 挂载点", "/dev/sda1", "/snap/core/1234", "ext4", false},
	}

	for _, c := range cases {
		if got := shouldIncludeMount(c.device, c.mountPoint, c.fsType); got != c.want {
			t.Errorf("%s: shouldIncludeMount(%q, %q, %q) = %v，期望 %v",
				c.name, c.device, c.mountPoint, c.fsType, got, c.want)
		}
	}
}

func TestFormatCapacityByGB(t *testing.T) {
	cases := map[float64]string{
		0.5:    "512.00 MB",
		1:      "1.00 GB",
		52.3:   "52.30 GB",
		1023.9: "1023.90 GB",
		1024:   "1.00 TB",
		2048:   "2.00 TB",
	}

	for value, want := range cases {
		if got := formatCapacityByGB(value); got != want {
			t.Errorf("formatCapacityByGB(%v) = %q，期望 %q", value, got, want)
		}
	}
}

func TestSortUsers(t *testing.T) {
	newUsers := func() []*userSummary {
		return []*userSummary{
			{username: "bob", totalGB: 10},
			{username: "alice", totalGB: 30},
			{username: "carol", totalGB: 20},
		}
	}

	cases := []struct {
		name    string
		by      string
		reverse bool
		want    []string
	}{
		{"默认按总量降序", "total", false, []string{"alice", "carol", "bob"}},
		{"总量升序", "total", true, []string{"bob", "carol", "alice"}},
		{"用户名升序", "user", false, []string{"alice", "bob", "carol"}},
		{"用户名降序", "user", true, []string{"carol", "bob", "alice"}},
		{"未知排序列按总量", "unknown", false, []string{"alice", "carol", "bob"}},
	}

	for _, c := range cases {
		users := newUsers()
		sortUsers(users, c.by, c.reverse)

		got := make([]string, len(users))
		for i, u := range users {
			got[i] = u.username
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: 得到 %v，期望 %v", c.name, got, c.want)
		}
	}
}

// ── 磁盘告警链路 ─────────────────────────────────────────────────────────────

func TestParseDiskUsageReport(t *testing.T) {
	report := strings.Join([]string{
		"# generated: 2026-07-25 01:00:03",
		"# columns: username\tdisk\tusage_gb\tfull_name",
		"alice\t/home\t12.30\t张三",
		"alice\t/data\t800.00\t张三",
		"bob\t/home\t3.50\t",
		"",
		"字段不足的行",
	}, "\n")

	got, err := parseDiskUsageReport(strings.NewReader(report))
	if err != nil {
		t.Fatalf("parseDiskUsageReport 返回错误: %v", err)
	}
	if got.generatedAt != "2026-07-25 01:00:03" {
		t.Errorf("统计时间 = %q", got.generatedAt)
	}
	if len(got.users) != 2 {
		t.Fatalf("期望 2 个用户，实际 %d 个: %+v", len(got.users), got.users)
	}

	// 保持首次出现顺序
	alice := got.users[0]
	if alice.username != "alice" || alice.fullName != "张三" {
		t.Errorf("第一个用户解析错误: %+v", alice)
	}
	// 多盘要累加
	if diff := alice.totalGB - 812.30; diff > 0.001 || diff < -0.001 {
		t.Errorf("总占用 = %v，期望 812.30", alice.totalGB)
	}
	if buildDetail(alice.disks) != "/home:12.30GB  /data:800.00GB" {
		t.Errorf("明细 = %q", buildDetail(alice.disks))
	}
	if got.users[1].fullName != "" {
		t.Errorf("没有全名时应为空串，得到 %q", got.users[1].fullName)
	}
}

func TestSelectOverQuotaUsers(t *testing.T) {
	users := []*userSummary{
		{username: "alice", totalGB: 812.30},
		{username: "bob", totalGB: 3.50},
		{username: "carol", totalGB: 500},
		{username: "dave", totalGB: 640},
	}

	over := selectOverQuotaUsers(users, 500)
	if len(over) != 2 {
		t.Fatalf("期望 2 个超标用户，实际 %d 个: %+v", len(over), over)
	}
	// 按占用降序
	if over[0].username != "alice" || over[1].username != "dave" {
		t.Errorf("排序错误: %s, %s", over[0].username, over[1].username)
	}
	// 恰好等于阈值不算超标
	for _, u := range over {
		if u.username == "carol" {
			t.Error("恰好等于阈值的用户不该算超标")
		}
	}
}

func TestRenderDiskWarning(t *testing.T) {
	// 无超标用户时返回空串，交由 writeMotdWarning 清除该来源
	if got := renderDiskWarning(nil, 500, "2026-07-25 01:00:03"); got != "" {
		t.Errorf("无超标用户时应返回空串，得到 %q", got)
	}

	users := []*userSummary{
		{username: "alice", fullName: "张三", totalGB: 812.30,
			disks: []diskEntry{{mount: "/data", usageGB: 800}}},
		{username: "bob", totalGB: 640},
	}

	got := renderDiskWarning(users, 500, "2026-07-25 01:00:03")
	for _, want := range []string{
		"超过 500.00 GB", // 阈值
		"alice (张三)",   // 有全名
		"812.30 GB",
		"/data:800.00GB", // 明细
		"2026-07-25 01:00:03",
		"disk usage",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("告警文本缺少 %q:\n%s", want, got)
		}
	}
	// 没有全名的用户不该出现空括号
	if strings.Contains(got, "bob ()") {
		t.Errorf("没有全名时不该出现空括号:\n%s", got)
	}

	// 报表没记统计时间时也不能输出 "统计时间: "
	noTime := renderDiskWarning(users, 500, "")
	if strings.Contains(noTime, "统计时间") {
		t.Errorf("没有统计时间时不该出现该字段:\n%s", noTime)
	}
}

func TestSelectOverThresholdMounts(t *testing.T) {
	usages := []DiskUsage{
		{MountPoint: "/", UsedPercent: 45.0},
		{MountPoint: "/home", UsedPercent: 88.1},
		{MountPoint: "/data", UsedPercent: 92.3},
		{MountPoint: "/boot", UsedPercent: 80.0},
	}

	over := selectOverThresholdMounts(usages, 80)
	if len(over) != 2 {
		t.Fatalf("期望 2 个超线分区，实际 %d 个: %+v", len(over), over)
	}
	// 按使用率降序，最紧张的排最前
	if over[0].MountPoint != "/data" || over[1].MountPoint != "/home" {
		t.Errorf("排序错误: %s, %s", over[0].MountPoint, over[1].MountPoint)
	}

	if len(selectOverThresholdMounts(usages, 95)) != 0 {
		t.Error("阈值高于所有分区时应返回空")
	}
	if len(selectOverThresholdMounts(nil, 80)) != 0 {
		t.Error("无挂载点时应返回空")
	}
}

func TestFilterUsersByName(t *testing.T) {
	users := []*userSummary{{username: "alice"}, {username: "bob"}}

	if got := filterUsersByName(users, "bob"); len(got) != 1 || got[0].username != "bob" {
		t.Errorf("filterUsersByName 结果错误: %+v", got)
	}
	if got := filterUsersByName(users, "nobody"); len(got) != 0 {
		t.Errorf("查不到的用户应返回空，得到 %+v", got)
	}
}
