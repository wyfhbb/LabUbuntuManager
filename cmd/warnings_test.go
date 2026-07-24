package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// useMotdDataFixture 把告警落盘路径指向夹具目录，测试结束后还原。
func useMotdDataFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	originalDir, originalLegacy := motdWarningsDir, legacyMotdWarningsFile
	motdWarningsDir = filepath.Join(root, "warnings.d")
	legacyMotdWarningsFile = filepath.Join(root, "warnings.txt")
	t.Cleanup(func() {
		motdWarningsDir, legacyMotdWarningsFile = originalDir, originalLegacy
	})
	return root
}

// 两个来源各写各的，互不覆盖 —— 这是把单文件拆成目录要解决的核心问题。
func TestWriteMotdWarningSourcesDoNotOverwriteEachOther(t *testing.T) {
	useMotdDataFixture(t)

	if err := writeMotdWarning(warningSourceInactive, "不活跃告警内容"); err != nil {
		t.Fatalf("写入不活跃告警失败: %v", err)
	}
	if err := writeMotdWarning(warningSourceDisk, "磁盘告警内容"); err != nil {
		t.Fatalf("写入磁盘告警失败: %v", err)
	}

	blocks := readMotdWarnings()
	if len(blocks) != 2 {
		t.Fatalf("期望读回 2 段告警，实际 %d 段: %v", len(blocks), blocks)
	}
	// 数字前缀决定顺序：10-disk 在 20-inactive 之前
	if blocks[0] != "磁盘告警内容" || blocks[1] != "不活跃告警内容" {
		t.Errorf("告警顺序或内容错误: %v", blocks)
	}
}

// 清除一个来源不应动到别的来源。
func TestWriteMotdWarningClearsOnlyItsOwnSource(t *testing.T) {
	useMotdDataFixture(t)

	writeMotdWarning(warningSourceDisk, "磁盘告警内容")
	writeMotdWarning(warningSourceInactive, "不活跃告警内容")

	if err := writeMotdWarning(warningSourceDisk, ""); err != nil {
		t.Fatalf("清除磁盘告警失败: %v", err)
	}

	blocks := readMotdWarnings()
	if len(blocks) != 1 || blocks[0] != "不活跃告警内容" {
		t.Errorf("清除磁盘告警后应只剩不活跃告警，得到 %v", blocks)
	}
	if _, err := os.Stat(warningFilePath(warningSourceDisk)); !os.IsNotExist(err) {
		t.Error("清除后文件应被删除")
	}
}

// 清除一个本来就不存在的来源不该报错（cron 每天都会跑一次 warn）。
func TestWriteMotdWarningClearMissingSourceIsNotAnError(t *testing.T) {
	useMotdDataFixture(t)

	if err := writeMotdWarning(warningSourceDisk, ""); err != nil {
		t.Errorf("清除不存在的来源不应报错，得到 %v", err)
	}
	if err := writeMotdWarning(warningSourceDisk, "   \n  "); err != nil {
		t.Errorf("只有空白的内容视为清除，不应报错，得到 %v", err)
	}
}

// 老机器上的单文件 warnings.txt 要迁进 warnings.d/，内容来自 user inactive。
func TestMigrateLegacyWarnings(t *testing.T) {
	useMotdDataFixture(t)

	if err := os.WriteFile(legacyMotdWarningsFile, []byte("⚠ 老的不活跃告警\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// 写入磁盘告警时顺带完成迁移
	if err := writeMotdWarning(warningSourceDisk, "磁盘告警内容"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	if _, err := os.Stat(legacyMotdWarningsFile); !os.IsNotExist(err) {
		t.Error("迁移后老文件应被删除")
	}
	blocks := readMotdWarnings()
	if len(blocks) != 2 {
		t.Fatalf("期望 2 段告警（磁盘 + 迁移来的不活跃），实际 %d 段: %v", len(blocks), blocks)
	}
	if blocks[1] != "⚠ 老的不活跃告警" {
		t.Errorf("老内容应迁到 %s，得到 %v", warningSourceInactive, blocks)
	}
}

// 迁移不能用老内容盖掉已经写过的新告警。
func TestMigrateLegacyWarningsDoesNotClobberNewer(t *testing.T) {
	useMotdDataFixture(t)

	writeMotdWarning(warningSourceInactive, "新的不活跃告警")
	if err := os.WriteFile(legacyMotdWarningsFile, []byte("过期的老告警\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := migrateLegacyWarnings(); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	blocks := readMotdWarnings()
	if len(blocks) != 1 || blocks[0] != "新的不活跃告警" {
		t.Errorf("已有的新告警不该被老内容覆盖，得到 %v", blocks)
	}
}

// 还没迁移过的机器上，读取路径要能同时看到老文件，避免升级后告警凭空消失。
func TestReadMotdWarningsIncludesUnmigratedLegacyFile(t *testing.T) {
	useMotdDataFixture(t)

	if err := os.WriteFile(legacyMotdWarningsFile, []byte("尚未迁移的告警\n"), 0644); err != nil {
		t.Fatal(err)
	}

	blocks := readMotdWarnings()
	if len(blocks) != 1 || blocks[0] != "尚未迁移的告警" {
		t.Errorf("readMotdWarnings 应兼容老文件，得到 %v", blocks)
	}
	// 只读不迁移：MOTD 渲染可能以普通用户身份执行，没有写权限
	if _, err := os.Stat(legacyMotdWarningsFile); err != nil {
		t.Error("读取路径不该删除老文件")
	}
}

func TestListMotdWarningSources(t *testing.T) {
	useMotdDataFixture(t)

	if sources := listMotdWarningSources(); len(sources) != 0 {
		t.Errorf("没有告警时应返回空，得到 %v", sources)
	}

	writeMotdWarning(warningSourceDisk, "磁盘告警内容")
	// 空文件不算有告警
	os.WriteFile(warningFilePath(warningSourceInactive), []byte("  \n"), 0644)

	sources := listMotdWarningSources()
	if len(sources) != 1 || !strings.Contains(sources[0], "磁盘占用超标") {
		t.Errorf("listMotdWarningSources = %v，期望只有磁盘一项", sources)
	}
}

// 走一遍完整链路：统计报表 → disk warn → MOTD 告警，同时验证不影响已有的
// 不活跃告警（roadmap 批次 2 的验收标准）。
func TestDiskWarningDoesNotDisturbInactiveWarning(t *testing.T) {
	useMotdDataFixture(t)

	// 不活跃告警已经在了
	inactive := renderInactiveWarning([]inactiveUser{
		{username: "olduser", fullName: "老用户", lastLogin: "2025-01-01", daysSince: 570},
	}, 180)
	if err := writeMotdWarning(warningSourceInactive, inactive); err != nil {
		t.Fatalf("写入不活跃告警失败: %v", err)
	}

	// 制造一个超标用户，跑一遍 disk warn 的逻辑
	report, err := parseDiskUsageReport(strings.NewReader(strings.Join([]string{
		"# generated: 2026-07-25 01:00:03",
		"hog\t/data\t900.00\t占盘的人",
		"tiny\t/home\t1.00\t老实人",
	}, "\n")))
	if err != nil {
		t.Fatalf("解析报表失败: %v", err)
	}

	over := selectOverQuotaUsers(report.users, 500)
	if err := writeMotdWarning(warningSourceDisk,
		renderDiskWarning(over, 500, report.generatedAt)); err != nil {
		t.Fatalf("写入磁盘告警失败: %v", err)
	}

	blocks := readMotdWarnings()
	if len(blocks) != 2 {
		t.Fatalf("期望磁盘与不活跃两段告警共存，实际 %d 段: %v", len(blocks), blocks)
	}
	if !strings.Contains(blocks[0], "hog") || strings.Contains(blocks[0], "tiny") {
		t.Errorf("磁盘告警应只点名超标用户: %q", blocks[0])
	}
	if !strings.Contains(blocks[1], "olduser") {
		t.Errorf("不活跃告警应完好无损: %q", blocks[1])
	}

	// 超标用户清理完之后，磁盘告警清除但不活跃告警仍在
	if err := writeMotdWarning(warningSourceDisk, renderDiskWarning(nil, 500, "")); err != nil {
		t.Fatalf("清除磁盘告警失败: %v", err)
	}
	blocks = readMotdWarnings()
	if len(blocks) != 1 || !strings.Contains(blocks[0], "olduser") {
		t.Errorf("清除磁盘告警后应只剩不活跃告警，得到 %v", blocks)
	}
}

// 分区超警戒线时 MOTD 顶部要给出醒目提示。
func TestRenderMotdDiskAlerts(t *testing.T) {
	usages := []DiskUsage{
		{MountPoint: "/", UsedPercent: 40, FreeGB: 300},
		{MountPoint: "/data", UsedPercent: 92.3, FreeGB: 45.2},
	}

	var buf bytes.Buffer
	renderMotdDiskAlerts(&buf, usages)

	got := buf.String()
	for _, want := range []string{"磁盘告警", "/data", "92.3%", "45.20 GB", "disk usage", colorRed} {
		if !strings.Contains(got, want) {
			t.Errorf("告警输出缺少 %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "/ 使用率") {
		t.Errorf("没超线的分区不该出现在顶部告警里:\n%s", got)
	}

	// 都没超线时整段不输出，MOTD 不能多出空行
	buf.Reset()
	renderMotdDiskAlerts(&buf, usages[:1])
	if buf.Len() != 0 {
		t.Errorf("无超线分区时应无输出，得到 %q", buf.String())
	}
}
