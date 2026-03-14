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
	diskCurrentReport = "/var/log/disk-usage/current-usage.txt"
	diskScriptDir     = "/usr/local/lib/server-mgr"
	diskScriptPath    = "/usr/local/lib/server-mgr/daily-disk-monitor.sh"
	diskCronFile      = "/etc/cron.d/server-mgr-disk"
	installedBinPath  = "/usr/local/bin/server-mgr"
	diskUsageWrapper  = "/usr/local/bin/disk-usage"
)

// monitorScript 在编译时从 shell/daily-disk-monitor.sh 嵌入。
// 需要修改脚本逻辑时，直接编辑该 sh 文件并重新编译即可。
//
//go:embed shell/daily-disk-monitor.sh
var monitorScript string

// cronContent 是写入 /etc/cron.d/ 的定时任务配置（/etc/cron.d 格式需含 user 字段）。
var cronContent = "# server-mgr 磁盘用量每日统计任务\n" +
	"# 由 server-mgr disk monitor enable 自动生成，请勿手动编辑\n" +
	"0 1 * * * root /bin/bash " + diskScriptPath + " >> " + diskLogDir + "/cron.log 2>&1\n"

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
	Short: "启用每日磁盘用量统计（每天 01:00 自动运行）",
	Run: func(cmd *cobra.Command, args []string) {
		if os.Getuid() != 0 {
			fmt.Fprintln(os.Stderr, "错误: 此命令需要 root 权限，请使用 sudo 执行")
			os.Exit(1)
		}

		// 1. 创建目录
		if err := os.MkdirAll(diskLogDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 无法创建日志目录 %s: %v\n", diskLogDir, err)
			os.Exit(1)
		}
		if err := os.MkdirAll(diskScriptDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 无法创建脚本目录 %s: %v\n", diskScriptDir, err)
			os.Exit(1)
		}

		// 2. 写出监控脚本
		if err := os.WriteFile(diskScriptPath, []byte(monitorScript), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 无法写入监控脚本 %s: %v\n", diskScriptPath, err)
			os.Exit(1)
		}
		fmt.Printf("监控脚本已写入: %s\n", diskScriptPath)

		// 3. 写出 cron 配置
		if err := os.WriteFile(diskCronFile, []byte(cronContent), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 无法写入定时任务配置 %s: %v\n", diskCronFile, err)
			os.Exit(1)
		}
		fmt.Printf("定时任务已配置: %s（每天 01:00 执行）\n", diskCronFile)

		// 4. 安装二进制到系统路径
		execPath, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: 无法获取当前二进制路径: %v\n", err)
			os.Exit(1)
		}
		if err := copyFile(execPath, installedBinPath, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 无法安装二进制到 %s: %v\n", installedBinPath, err)
			os.Exit(1)
		}
		fmt.Printf("二进制已安装: %s\n", installedBinPath)

		// 5. 写出 disk-usage 快捷命令供所有用户使用
		if err := os.WriteFile(diskUsageWrapper, []byte(wrapperScript), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 无法写入快捷命令 %s: %v\n", diskUsageWrapper, err)
			os.Exit(1)
		}
		fmt.Printf("快捷命令已安装: %s\n", diskUsageWrapper)

		// 6. 立即执行一次统计
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
			fmt.Println("定时任务:  已启用（每天 01:00 自动执行）")
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

// ── disk usage 子命令 ─────────────────────────────────────────────────────────

var diskUsageCmd = &cobra.Command{
	Use:   "usage",
	Short: "查看各用户当日磁盘使用量（所有用户可用）",
	Run: func(cmd *cobra.Command, args []string) {
		onlyMe, _ := cmd.Flags().GetBool("me")
		sortBy, _ := cmd.Flags().GetString("sort")
		reverse, _ := cmd.Flags().GetBool("reverse")

		f, err := os.Open(diskCurrentReport)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, "错误: 暂无统计数据，请联系管理员执行: sudo server-mgr disk monitor enable")
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "错误: 读取报告失败: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()

		// 尽量准确获取当前登录用户（sudo 下 USER 可能为 root）
		currentUser := os.Getenv("SUDO_USER")
		if currentUser == "" {
			currentUser = os.Getenv("USER")
		}
		if currentUser == "" {
			currentUser = os.Getenv("LOGNAME")
		}

		// ── 解析报告文件，按用户汇总 ──────────────────────────────────────
		generatedAt := ""
		userMap := map[string]*userSummary{}
		var userOrder []string // 保持首次出现顺序，便于按 user 排序时稳定

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if rest, ok := strings.CutPrefix(line, "# generated: "); ok {
				generatedAt = rest
				continue
			}
			if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
				continue
			}
			// 格式：username <TAB> disk <TAB> usage_gb <TAB> full_name
			fields := strings.SplitN(line, "\t", 4)
			if len(fields) < 3 {
				continue
			}
			username := fields[0]
			if onlyMe && username != currentUser {
				continue
			}

			var gb float64
			fmt.Sscanf(fields[2], "%f", &gb)

			fullName := ""
			if len(fields) >= 4 {
				fullName = fields[3]
			}

			u, exists := userMap[username]
			if !exists {
				u = &userSummary{username: username, fullName: fullName}
				userMap[username] = u
				userOrder = append(userOrder, username)
			}
			u.totalGB += gb
			u.disks = append(u.disks, diskEntry{mount: fields[1], usageGB: gb})
		}
		if err := scanner.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 读取报告时出错: %v\n", err)
			os.Exit(1)
		}

		// 转为切片以排序
		users := make([]*userSummary, 0, len(userOrder))
		for _, name := range userOrder {
			users = append(users, userMap[name])
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

	diskCmd.AddCommand(diskMonitorCmd)
	diskCmd.AddCommand(diskUsageCmd)
}
