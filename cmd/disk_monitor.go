package cmd

import (
	"bufio"
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

const (
	diskLogDir        = "/var/log/disk-usage"
	diskCurrentReport = diskLogDir + "/current-usage.txt"
	diskScriptPath    = serverMgrLibDir + "/daily-disk-monitor.sh"
	diskCronFile      = "/etc/cron.d/server-mgr-disk"
	installedBinPath  = "/usr/local/bin/server-mgr"
	diskUsageWrapper  = "/usr/local/bin/disk-usage"
)

// monitorScript 在编译时从 shell/daily-disk-monitor.sh 嵌入。
// 需要修改脚本逻辑时，直接编辑该 sh 文件并重新编译即可。
//
//go:embed shell/daily-disk-monitor.sh
var monitorScript string

// diskCronContent 生成写入 /etc/cron.d/ 的定时任务配置
// （/etc/cron.d 格式需含 user 字段）。执行时间取自 config.conf。
func diskCronContent(cfg serverMgrConfig) string {
	return "# server-mgr 磁盘用量每日统计任务\n" +
		"# 由 server-mgr disk monitor enable 自动生成，请勿手动编辑\n" +
		cronExpr(cfg.DiskCronTime) + " root /bin/bash " + diskScriptPath + " >> " + diskLogDir + "/cron.log 2>&1\n"
}

// wrapperScript 写入 /usr/local/bin/disk-usage，供所有用户直接调用。
var wrapperScript = "#!/bin/bash\n" +
	"# 磁盘用量快捷查看工具，由 server-mgr disk monitor enable 自动安装\n" +
	"exec " + installedBinPath + " disk usage \"$@\"\n"

// ── 辅助：复制文件 ─────────────────────────────────────────────────────────────

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// ── monitor 子命令组 ──────────────────────────────────────────────────────────

var diskMonitorCmd = &cobra.Command{
	Use:   "monitor",
	Short: "每日磁盘用量统计定时任务管理（需要 root）",
}

var diskMonitorEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "启用每日磁盘用量统计（执行时间取自 config.conf，默认每天 01:00）",
	Run: func(cmd *cobra.Command, args []string) {
		requireRoot()

		cfg := config()

		// 1. 创建目录
		if err := os.MkdirAll(diskLogDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 无法创建日志目录 %s: %v\n", diskLogDir, err)
			os.Exit(1)
		}
		if err := os.MkdirAll(serverMgrLibDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 无法创建脚本目录 %s: %v\n", serverMgrLibDir, err)
			os.Exit(1)
		}

		// 2. 写出监控脚本
		if err := os.WriteFile(diskScriptPath, []byte(monitorScript), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 无法写入监控脚本 %s: %v\n", diskScriptPath, err)
			os.Exit(1)
		}
		fmt.Printf("监控脚本已写入: %s\n", diskScriptPath)

		// 3. 写出 cron 配置
		if err := os.WriteFile(diskCronFile, []byte(diskCronContent(cfg)), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 无法写入定时任务配置 %s: %v\n", diskCronFile, err)
			os.Exit(1)
		}
		fmt.Printf("定时任务已配置: %s（每天 %s 执行）\n", diskCronFile, cronTimeDisplay(cfg.DiskCronTime))

		// 4. 安装二进制到系统路径
		if err := ensureInstalled(); err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}

		// 5. 写出 disk-usage 快捷命令供所有用户使用
		if err := installDiskUsageWrapper(); err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("快捷命令已安装: %s\n", diskUsageWrapper)

		// 6. 写出默认配置（缺失时）
		if created, err := ensureConfigFile(); err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		} else if created {
			fmt.Printf("默认配置已写入: %s\n", configFilePath)
		}

		// 7. 立即执行一次统计
		fmt.Println("正在立即执行一次统计，请稍候...")
		if err := runMonitorScript(); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 首次统计执行失败: %v\n", err)
			os.Exit(1)
		}

		fmt.Println()
		fmt.Println("已启用。所有用户可通过以下任意方式查看统计结果：")
		fmt.Println("  disk usage            # 快捷命令（任意用户）")
		fmt.Println("  disk usage --me       # 只看自己")
		fmt.Println("  server-mgr disk usage # 完整命令")
	},
}

var diskMonitorDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "禁用每日磁盘用量统计定时任务",
	Run: func(cmd *cobra.Command, args []string) {
		if os.Getuid() != 0 {
			fmt.Fprintln(os.Stderr, "错误: 此命令需要 root 权限，请使用 sudo 执行")
			os.Exit(1)
		}

		if err := os.Remove(diskCronFile); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "错误: 无法删除定时任务配置: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("定时任务已删除")

		if err := os.Remove(diskUsageWrapper); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "错误: 无法删除快捷命令 %s: %v\n", diskUsageWrapper, err)
			os.Exit(1)
		}
		fmt.Println("快捷命令 disk usage 已删除")

		fmt.Println("已禁用。历史统计数据仍保留在", diskLogDir)
	},
}

var diskMonitorStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看每日磁盘用量统计定时任务状态",
	Run: func(cmd *cobra.Command, args []string) {
		if _, err := os.Stat(diskCronFile); err == nil {
			fmt.Printf("定时任务:  已启用（每天 %s 自动执行）\n", cronTimeDisplay(config().DiskCronTime))
		} else {
			fmt.Println("定时任务:  未启用")
			fmt.Println("启用方法:  sudo server-mgr disk monitor enable")
		}

		if _, err := os.Stat(diskUsageWrapper); err == nil {
			fmt.Printf("快捷命令:  已安装 %s\n", diskUsageWrapper)
		} else {
			fmt.Println("快捷命令:  未安装")
		}

		fmt.Printf("日志目录:  %s\n", diskLogDir)
		if info, err := os.Stat(diskCurrentReport); err == nil {
			fmt.Printf("最近统计:  %s\n", info.ModTime().Format("2006-01-02 15:04:05"))
		} else {
			fmt.Println("最近统计:  暂无数据")
		}
	},
}

var diskMonitorRunCmd = &cobra.Command{
	Use:   "run",
	Short: "立即执行一次磁盘用量统计（需要 root）",
	Run: func(cmd *cobra.Command, args []string) {
		if os.Getuid() != 0 {
			fmt.Fprintln(os.Stderr, "错误: 此命令需要 root 权限，请使用 sudo 执行")
			os.Exit(1)
		}
		if _, err := os.Stat(diskScriptPath); os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "错误: 监控脚本不存在，请先执行 sudo server-mgr disk monitor enable")
			os.Exit(1)
		}
		fmt.Println("正在执行统计，请稍候...")
		if err := runMonitorScript(); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 统计执行失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("统计完成。")
	},
}

func runMonitorScript() error {
	c := exec.Command("/bin/bash", diskScriptPath)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// diskEntry 表示某用户在某块盘上的使用量。
type diskEntry struct {
	mount   string
	usageGB float64
}

// userSummary 汇总单个用户在所有盘上的使用情况。
type userSummary struct {
	username string
	fullName string
	totalGB  float64
	disks    []diskEntry // 按出现顺序保留，便于展示明细
}

// diskUsageReport 是每日统计报表的解析结果。
type diskUsageReport struct {
	generatedAt string
	users       []*userSummary // 保持首次出现顺序，便于按 user 排序时稳定
}

// parseDiskUsageReport 解析 daily-disk-monitor.sh 产出的报表并按用户汇总。
//
// 格式：username <TAB> mount_point <TAB> usage_gb <TAB> full_name，
// 以 # 开头的是注释，其中 "# generated: " 带统计时间。
func parseDiskUsageReport(r io.Reader) (diskUsageReport, error) {
	report := diskUsageReport{}
	byUser := map[string]*userSummary{}

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if rest, ok := strings.CutPrefix(line, "# generated: "); ok {
			report.generatedAt = rest
			continue
		}
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}

		fields := strings.SplitN(line, "\t", 4)
		if len(fields) < 3 {
			continue
		}
		username := fields[0]

		var gb float64
		fmt.Sscanf(fields[2], "%f", &gb)

		fullName := ""
		if len(fields) >= 4 {
			fullName = fields[3]
		}

		u, exists := byUser[username]
		if !exists {
			u = &userSummary{username: username, fullName: fullName}
			byUser[username] = u
			report.users = append(report.users, u)
		}
		u.totalGB += gb
		u.disks = append(u.disks, diskEntry{mount: fields[1], usageGB: gb})
	}
	if err := scanner.Err(); err != nil {
		return report, fmt.Errorf("读取报告时出错: %w", err)
	}
	return report, nil
}

// openDiskUsageReport 打开每日统计报表，缺失时给出启用统计的指引。
func openDiskUsageReport() (*os.File, error) {
	f, err := os.Open(diskCurrentReport)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("暂无统计数据，请先执行: sudo server-mgr disk monitor enable")
	}
	if err != nil {
		return nil, fmt.Errorf("读取报告失败: %w", err)
	}
	return f, nil
}

// currentInvokingUser 尽量准确地取当前登录用户（sudo 下 USER 可能是 root）。
func currentInvokingUser() string {
	for _, key := range []string{"SUDO_USER", "USER", "LOGNAME"} {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return ""
}

// ── disk usage 子命令 ─────────────────────────────────────────────────────────

var diskUsageCmd = &cobra.Command{
	Use:   "usage",
	Short: "查看各用户当日磁盘使用量（所有用户可用）",
	Run: func(cmd *cobra.Command, args []string) {
		onlyMe, _ := cmd.Flags().GetBool("me")
		sortBy, _ := cmd.Flags().GetString("sort")
		reverse, _ := cmd.Flags().GetBool("reverse")

		f, err := openDiskUsageReport()
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()

		report, err := parseDiskUsageReport(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}
		generatedAt := report.generatedAt

		currentUser := currentInvokingUser()

		users := report.users
		if onlyMe {
			users = filterUsersByName(users, currentUser)
		}

		// ── 排序 ──────────────────────────────────────────────────────────
		sortUsers(users, sortBy, reverse)

		// ── 展示 ──────────────────────────────────────────────────────────
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  用户名\t全名\t总计(GB)\t明细")
		fmt.Fprintln(tw, "  ------\t----\t--------\t----")
		for _, u := range users {
			marker := "  "
			if u.username == currentUser {
				marker = "* "
			}
			detail := buildDetail(u.disks)
			fmt.Fprintf(tw, "%s%s\t%s\t%.2f\t%s\n", marker, u.username, u.fullName, u.totalGB, detail)
		}
		tw.Flush()

		if generatedAt != "" {
			fmt.Printf("\n数据更新时间: %s\n", generatedAt)
		}
	},
}

// sortUsers 按指定列排序用户列表。默认按总量降序，--reverse 反转。
func sortUsers(users []*userSummary, by string, reverse bool) {
	less := func(i, j int) bool {
		switch by {
		case "user":
			if reverse {
				return users[i].username > users[j].username
			}
			return users[i].username < users[j].username
		default: // "total" 及其他：按总量
			if reverse {
				return users[i].totalGB < users[j].totalGB
			}
			return users[i].totalGB > users[j].totalGB
		}
	}
	// 简单插入排序（用户数通常很少）
	for i := 1; i < len(users); i++ {
		for j := i; j > 0 && less(j, j-1); j-- {
			users[j], users[j-1] = users[j-1], users[j]
		}
	}
}

// filterUsersByName 只保留指定用户，供 disk usage --me 使用。
func filterUsersByName(users []*userSummary, username string) []*userSummary {
	var result []*userSummary
	for _, u := range users {
		if u.username == username {
			result = append(result, u)
		}
	}
	return result
}

// ── disk warn 子命令 ──────────────────────────────────────────────────────────

var diskWarnGB float64

var diskWarnCmd = &cobra.Command{
	Use:   "warn",
	Short: "将磁盘占用超标的用户写入 MOTD 警告（需要 root）",
	Long: `读取每日统计报表，把总占用超过阈值的用户点名写入 MOTD，
所有用户登录时都能看到是谁把盘占满了。

数据来源是每日统计报表，需要先启用统计：
  sudo server-mgr disk monitor enable

统计脚本跑完后会自动触发一次，通常不需要手动执行。
阈值默认取自 config.conf 的 DISK_USER_WARN_GB。

分区使用率超警戒线是另一条链路：实时计算，直接在 MOTD 顶部提示，
不需要也不受本命令影响。

示例：
  sudo server-mgr disk warn
  sudo server-mgr disk warn --gb 200`,
	Run: func(cmd *cobra.Command, args []string) {
		requireRoot()

		f, err := openDiskUsageReport()
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()

		report, err := parseDiskUsageReport(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}

		over := selectOverQuotaUsers(report.users, diskWarnGB)
		if err := writeMotdWarning(warningSourceDisk, renderDiskWarning(over, diskWarnGB, report.generatedAt)); err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}

		if len(over) == 0 {
			fmt.Printf("没有用户占用超过 %s，磁盘告警已清除\n", formatCapacityByGB(diskWarnGB))
			return
		}

		fmt.Printf("已将 %d 个占用超过 %s 的用户写入 MOTD\n", len(over), formatCapacityByGB(diskWarnGB))
		fmt.Printf("警告文件: %s\n", warningFilePath(warningSourceDisk))
		fmt.Println()
		fmt.Println("预览效果: server-mgr motd show")
	},
}

// selectOverQuotaUsers 挑出总占用超过阈值的用户，按占用降序。
func selectOverQuotaUsers(users []*userSummary, thresholdGB float64) []*userSummary {
	var over []*userSummary
	for _, u := range users {
		if u.totalGB > thresholdGB {
			over = append(over, u)
		}
	}
	sortUsers(over, "total", false)
	return over
}

// renderDiskWarning 生成写入 MOTD 的磁盘占用告警文本。
// 没有超标用户时返回空串，交由 writeMotdWarning 清除该来源。
func renderDiskWarning(users []*userSummary, thresholdGB float64, generatedAt string) string {
	if len(users) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(colorBold + colorYellow +
		fmt.Sprintf("⚠ 以下用户磁盘占用超过 %s", formatCapacityByGB(thresholdGB)) + colorReset + "\n")
	for _, u := range users {
		sb.WriteString(fmt.Sprintf("  %s — 共 %s  %s\n",
			formatUserLabel(u.username, u.fullName), formatCapacityByGB(u.totalGB), buildDetail(u.disks)))
	}

	hint := "  完整排行: disk usage"
	if generatedAt != "" {
		hint = fmt.Sprintf("  统计时间: %s，完整排行: disk usage", generatedAt)
	}
	sb.WriteString(colorDim + hint + colorReset)
	return sb.String()
}

// buildDetail 将各盘使用量拼成易读字符串，如 "/home:0.12GB  /workspace:52.30GB"。
func buildDetail(disks []diskEntry) string {
	parts := make([]string, 0, len(disks))
	for _, d := range disks {
		parts = append(parts, fmt.Sprintf("%s:%.2fGB", d.mount, d.usageGB))
	}
	return strings.Join(parts, "  ")
}

func init() {
	diskMonitorCmd.AddCommand(diskMonitorEnableCmd)
	diskMonitorCmd.AddCommand(diskMonitorDisableCmd)
	diskMonitorCmd.AddCommand(diskMonitorStatusCmd)
	diskMonitorCmd.AddCommand(diskMonitorRunCmd)

	diskUsageCmd.Flags().BoolP("me", "m", false, "只显示当前用户的使用情况")
	diskUsageCmd.Flags().StringP("sort", "s", "total", "排序列：total（总量，默认）或 user（用户名）")
	diskUsageCmd.Flags().BoolP("reverse", "r", false, "反向排序")

	// 默认值取自 config.conf，cron 里不带 --gb 调用时拿到的就是管理员配置的阈值
	warnGB := config().DiskUserWarnGB
	diskWarnCmd.Flags().Float64Var(&diskWarnGB, "gb", warnGB,
		fmt.Sprintf("用户总占用超过多少 GB 则写入警告（默认 %g）", warnGB))

	diskCmd.AddCommand(diskMonitorCmd)
	diskCmd.AddCommand(diskUsageCmd)
	diskCmd.AddCommand(diskWarnCmd)
}
