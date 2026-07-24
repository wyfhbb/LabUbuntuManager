package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

const (
	// 默认不活跃天数阈值
	defaultInactiveDays = 180
	// inactive 监控相关路径
	inactiveConfFile = motdDataDir + "/inactive-days.conf"
	inactiveCronFile = "/etc/cron.d/server-mgr-inactive"
)

// inactiveUser 表示一个用户及其登录状态。
type inactiveUser struct {
	username  string
	fullName  string
	lastLogin string // "YYYY-MM-DD" 或 "从未登录"
	daysSince int
}

// ── user inactive list ────────────────────────────────────────────────────────

var userInactiveListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出所有用户的未登录天数",
	Long: `列出所有普通用户及其未登录天数。

示例：
  server-mgr user inactive list

数据来源：lastlog 命令`,
	Run: func(cmd *cobra.Command, args []string) {
		if os.Getuid() != 0 {
			fmt.Fprintln(os.Stderr, "错误: 此命令需要 root 权限，请使用 sudo 执行")
			os.Exit(1)
		}

		users := collectAllUsers()
		if len(users) == 0 {
			fmt.Println("没有找到普通用户")
			return
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "用户名\t全名\t最后登录\t未登录天数")
		fmt.Fprintln(tw, "------\t----\t--------\t----------")

		for _, u := range users {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d 天\n",
				u.username, u.fullName, u.lastLogin, u.daysSince)
		}
		tw.Flush()

		fmt.Println()
		fmt.Println("写入 MOTD 警告:  sudo server-mgr user inactive warn")
		fmt.Println("删除不活跃用户:  sudo server-mgr user inactive purge --days 180")
		fmt.Println("启用定时检查:    sudo server-mgr user inactive monitor enable --days 180")
	},
}

// ── user inactive warn ────────────────────────────────────────────────────────

var inactiveWarnDays int

var userInactiveWarnCmd = &cobra.Command{
	Use:   "warn",
	Short: "将长期不登录用户警告写入 MOTD（需要 root）",
	Long: `将超过指定天数未登录的用户信息写入 MOTD，所有用户登录时将看到警告。

示例：
  sudo server-mgr user inactive warn
  sudo server-mgr user inactive warn --days 120

先用 user inactive list 查看各用户未登录天数。`,
	Run: func(cmd *cobra.Command, args []string) {
		if os.Getuid() != 0 {
			fmt.Fprintln(os.Stderr, "错误: 此命令需要 root 权限，请使用 sudo 执行")
			os.Exit(1)
		}

		// 未显式传 --days 时，默认值本身就来自 config.conf（见 init）
		days := inactiveWarnDays

		users := collectInactiveUsers(days)
		if err := writeMotdWarning(warningSourceInactive, renderInactiveWarning(users, days)); err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}

		if len(users) == 0 {
			fmt.Printf("没有超过 %d 天未登录的用户，不活跃告警已清除\n", days)
			return
		}

		fmt.Printf("已将 %d 个超过 %d 天未登录的用户警告写入 MOTD\n", len(users), days)
		fmt.Printf("警告文件: %s\n", warningFilePath(warningSourceInactive))
		fmt.Println()
		fmt.Println("所有用户下次登录时将看到此警告。")
		fmt.Println("预览效果: server-mgr motd show")
	},
}

// ── user inactive purge ──────────────────────────────────────────────────────

var inactivePurgeDaysFlag int

var userInactivePurgeCmd = &cobra.Command{
	Use:   "purge",
	Short: "删除超过指定天数未登录的用户（需要 root）",
	Long: `删除超过指定天数未登录的用户，同时清理家目录和数据盘目录。
删除完成后自动更新 MOTD 警告。

示例：
  sudo server-mgr user inactive purge --days 180

⚠ 此操作不可逆！建议先执行 user inactive list 查看各用户未登录天数。`,
	Run: func(cmd *cobra.Command, args []string) {
		if os.Getuid() != 0 {
			fmt.Fprintln(os.Stderr, "错误: 此命令需要 root 权限，请使用 sudo 执行")
			os.Exit(1)
		}

		users := collectInactiveUsers(inactivePurgeDaysFlag)
		if len(users) == 0 {
			fmt.Printf("没有超过 %d 天未登录的用户\n", inactivePurgeDaysFlag)
			return
		}

		// 列出将要删除的用户
		fmt.Printf("以下 %d 个用户超过 %d 天未登录，将被删除：\n\n", len(users), inactivePurgeDaysFlag)
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  用户名\t全名\t最后登录\t未登录天数")
		fmt.Fprintln(tw, "  ------\t----\t--------\t----------")
		for _, u := range users {
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%d 天\n", u.username, u.fullName, u.lastLogin, u.daysSince)
		}
		tw.Flush()

		fmt.Print("\n⚠ 此操作将删除以上用户及其所有数据，不可恢复！确认删除? (y/N): ")
		var confirm string
		fmt.Scanln(&confirm)
		if strings.TrimSpace(strings.ToLower(confirm)) != "y" {
			fmt.Println("已取消")
			return
		}

		// 获取数据盘挂载点（复用 user del 的逻辑）
		var dataMounts []DiskUsage
		if provider := NewProcMountDiskUsageProvider(); provider != nil {
			if all, err := provider.ListDiskUsage(); err == nil {
				dataMounts = dataMountCandidates(all)
			}
		}

		deleted := 0
		for _, u := range users {
			fmt.Printf("\n正在删除用户 %s ... \n", u.username)

			// 收集数据盘目录（在 userdel 之前）
			var dataDirs []string
			for _, m := range dataMounts {
				dir := filepath.Join(m.MountPoint, u.username)
				if _, err := os.Stat(dir); err == nil {
					dataDirs = append(dataDirs, dir)
				}
			}

			// 删除系统用户（-r 一并删家目录）
			delCmd := exec.Command("userdel", "-r", u.username)
			delCmd.Stdout = os.Stdout
			delCmd.Stderr = os.Stderr
			if err := delCmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "错误: 删除用户 %s 失败: %v\n", u.username, err)
				continue
			}

			// 清理各数据盘上的用户目录
			for _, dir := range dataDirs {
				fmt.Printf("  正在删除 %s ... ", dir)
				if err := os.RemoveAll(dir); err != nil {
					fmt.Fprintf(os.Stderr, "\n错误: 删除 %s 失败: %v\n", dir, err)
					continue
				}
				fmt.Println("完成")
			}

			fmt.Printf("用户 %s 已删除\n", u.username)
			deleted++
		}

		// 自动更新 MOTD 警告（重新检查剩余不活跃用户）
		updateMotdWarnings(inactivePurgeDaysFlag)

		fmt.Printf("\n已完成，共删除 %d 个用户\n", deleted)
	},
}

// ── user inactive monitor ────────────────────────────────────────────────────

var inactiveMonitorDays int

var inactiveMonitorCmd = &cobra.Command{
	Use:   "monitor",
	Short: "不活跃用户定时检查任务管理（需要 root）",
}

var inactiveMonitorEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "启用不活跃用户定时检查（每天 02:00 自动执行）",
	Long: `启用定时检查，每天 02:00 自动扫描不活跃用户并写入 MOTD 警告。
同时安装二进制到系统路径。

示例：
  sudo server-mgr user inactive monitor enable --days 180`,
	Run: func(cmd *cobra.Command, args []string) {
		requireRoot()

		days := inactiveMonitorDays

		// 1. 确保目录存在
		if err := os.MkdirAll(motdDataDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 无法创建目录 %s: %v\n", motdDataDir, err)
			os.Exit(1)
		}

		// 2. 保存天数配置（统一落在 config.conf，不再单独一个文件）
		if _, err := ensureConfigFile(); err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}
		cfg := readConfigFile(configFilePath)
		cfg.InactiveDays = days
		if err := writeConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("阈值已保存: %d 天 → %s\n", days, configFilePath)

		// 3. 安装二进制到系统路径
		if err := ensureInstalled(); err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}

		// 4. 写入 cron 配置（执行时间取自 config.conf）
		cronContent := "# server-mgr 不活跃用户定时检查（每天 " + cronTimeDisplay(cfg.InactiveCronTime) + "）\n" +
			"# 由 server-mgr user inactive monitor enable 自动生成，请勿手动编辑\n" +
			"LANG=C\n" +
			cronExpr(cfg.InactiveCronTime) + " root " + installedBinPath + " user inactive warn 2>/dev/null\n"
		if err := os.WriteFile(inactiveCronFile, []byte(cronContent), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 无法写入 cron 配置 %s: %v\n", inactiveCronFile, err)
			os.Exit(1)
		}
		fmt.Printf("定时任务已配置: %s（每天 %s 执行）\n", inactiveCronFile, cronTimeDisplay(cfg.InactiveCronTime))

		// 5. 立即执行一次检查
		fmt.Print("正在执行首次检查... ")
		users := collectInactiveUsers(days)
		if err := writeMotdWarning(warningSourceInactive, renderInactiveWarning(users, days)); err != nil {
			fmt.Fprintf(os.Stderr, "\n错误: %v\n", err)
			os.Exit(1)
		}
		if len(users) == 0 {
			fmt.Printf("完成（无超过 %d 天未登录的用户）\n", days)
		} else {
			fmt.Printf("完成（%d 个用户已写入 MOTD）\n", len(users))
		}

		fmt.Println()
		fmt.Println("已启用。相关命令：")
		fmt.Println("  查看状态:  server-mgr user inactive monitor status")
		fmt.Println("  手动触发:  sudo server-mgr user inactive warn")
		fmt.Println("  禁用定时:  sudo server-mgr user inactive monitor disable")
	},
}

var inactiveMonitorDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "禁用不活跃用户定时检查",
	Run: func(cmd *cobra.Command, args []string) {
		if os.Getuid() != 0 {
			fmt.Fprintln(os.Stderr, "错误: 此命令需要 root 权限，请使用 sudo 执行")
			os.Exit(1)
		}

		if err := os.Remove(inactiveCronFile); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "错误: 无法删除定时任务配置: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("定时任务已删除")
		fmt.Printf("配置文件保留在 %s，如需清理请手动删除\n", configFilePath)
	},
}

var inactiveMonitorStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看不活跃用户定时检查任务状态",
	Run: func(cmd *cobra.Command, args []string) {
		cfg := config()

		if _, err := os.Stat(inactiveCronFile); err == nil {
			fmt.Printf("定时任务:  已启用（每天 %s 自动检查）\n", cronTimeDisplay(cfg.InactiveCronTime))
		} else {
			fmt.Println("定时任务:  未启用")
			fmt.Printf("启用方法:  sudo server-mgr user inactive monitor enable --days %d\n", cfg.InactiveDays)
		}

		fmt.Printf("阈值天数:  %d 天\n", cfg.InactiveDays)

		path := warningFilePath(warningSourceInactive)
		if info, err := os.Stat(path); err == nil {
			fmt.Printf("MOTD 警告:  已写入（%s）\n", info.ModTime().Format("2006-01-02 15:04:05"))
			fmt.Printf("           %s\n", path)
		} else {
			fmt.Println("MOTD 警告:  无")
		}
	},
}

// ── 辅助 ─────────────────────────────────────────────────────────────────────

// readInactiveConf 读取保存的天数阈值配置。
func readInactiveConf() int {
	data, err := os.ReadFile(inactiveConfFile)
	if err != nil {
		return 0
	}
	var days int
	fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &days)
	return days
}

// renderInactiveWarning 生成写入 MOTD 的不活跃用户告警文本。
// 没有不活跃用户时返回空串，交由 writeMotdWarning 清除该来源。
func renderInactiveWarning(users []inactiveUser, days int) string {
	if len(users) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(colorBold + colorYellow + fmt.Sprintf("⚠ 以下用户超过 %d 天未登录", days) + colorReset + "\n")
	for _, u := range users {
		sb.WriteString(fmt.Sprintf("  %s — 最后登录: %s (%d 天前)\n",
			formatUserLabel(u.username, u.fullName), u.lastLogin, u.daysSince))
	}
	sb.WriteString(colorDim + fmt.Sprintf("  管理员可执行: sudo server-mgr user inactive purge --days %d", days) + colorReset)
	return sb.String()
}

// formatUserLabel 拼出 "用户名 (全名)"，没有全名时只给用户名。
func formatUserLabel(username, fullName string) string {
	if strings.TrimSpace(fullName) == "" {
		return username
	}
	return fmt.Sprintf("%s (%s)", username, fullName)
}

// updateMotdWarnings 重新检查不活跃用户并更新 MOTD 告警。
func updateMotdWarnings(days int) {
	users := collectInactiveUsers(days)
	if err := writeMotdWarning(warningSourceInactive, renderInactiveWarning(users, days)); err != nil {
		fmt.Fprintf(os.Stderr, "警告: %v\n", err)
		return
	}
	if len(users) == 0 {
		fmt.Println("不活跃告警已清除（无不活跃用户）")
		return
	}
	fmt.Printf("MOTD 警告已更新（%d 个不活跃用户）\n", len(users))
}

// ── 核心逻辑 ─────────────────────────────────────────────────────────────────

// collectAllUsers 收集所有普通用户及其登录状态。
// 活跃时间检测综合三个来源（取最晚）：
//  1. lastlog：最后一次登录时间
//  2. ps：用户是否有运行中进程（进程启动时间）
//  3. /home/<user> 下最近修改的文件
func collectAllUsers() []inactiveUser {
	type user struct {
		username string
		fullName string
	}

	f, err := os.Open("/etc/passwd")
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: 读取 /etc/passwd 失败: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	var normalUsers []user
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) < 7 {
			continue
		}
		username, uidStr, gecos, shell := parts[0], parts[2], parts[4], parts[6]
		var uid int
		fmt.Sscanf(uidStr, "%d", &uid)
		if uid < 1000 {
			continue
		}
		if shell == "/usr/sbin/nologin" || shell == "/bin/false" {
			continue
		}
		fullName := strings.SplitN(gecos, ",", 2)[0]
		normalUsers = append(normalUsers, user{username, fullName})
	}

	if len(normalUsers) == 0 {
		return nil
	}

	userNames := make(map[string]bool, len(normalUsers))
	for _, u := range normalUsers {
		userNames[u.username] = true
	}

	// 信号1：lastlog 最后登录时间
	lastLogins := parseLastlog(userNames)

	// 信号2：各用户进程情况（有进程 = 最近活跃）
	var userNamesList []string
	for _, u := range normalUsers {
		userNamesList = append(userNamesList, u.username)
	}
	hasProcess := detectUserProcesses(userNamesList)

	now := time.Now()
	var result []inactiveUser

	for _, u := range normalUsers {
		createTime := time.Time{}
		lastTime, hasLogin := lastLogins[u.username]
		if !hasLogin {
			createTime = getUserCreateTime(u.username)
		}
		homeMtime := time.Time{}
		if hasLogin {
			homeMtime = getHomeRecentMtime(u.username)
		}
		lastTime = resolveInactiveLastTime(now, lastTime, createTime, homeMtime, hasLogin, hasProcess[u.username])

		daysSince := int(now.Sub(lastTime).Hours() / 24)
		if daysSince < 0 {
			daysSince = 0
		}

		result = append(result, inactiveUser{
			username:  u.username,
			fullName:  u.fullName,
			lastLogin: formatInactiveLastLogin(lastTime, createTime, hasLogin),
			daysSince: daysSince,
		})
	}

	return result
}

// detectUserProcesses 检测哪些用户当前有运行中的进程。
func detectUserProcesses(userNames []string) map[string]bool {
	result := make(map[string]bool)

	// ps -eo user= 输出每个进程的所有者
	out, err := exec.Command("ps", "-eo", "user=").Output()
	if err != nil {
		return result
	}

	activeUsers := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name != "" {
			activeUsers[name] = true
		}
	}

	for _, name := range userNames {
		result[name] = activeUsers[name]
	}

	return result
}

// getHomeRecentMtime 获取用户 home 目录下最近修改的文件时间。
// 只检查 maxdepth=2（home 直接子目录），避免递归太深影响性能。
func getHomeRecentMtime(username string) time.Time {
	homeDir := filepath.Join("/home", username)
	if _, err := os.Stat(homeDir); err != nil {
		return time.Time{} // 零值，不会影响 max 比较
	}

	// 用 find 获取最近修改的文件，限制 depth=2，取最新一个
	out, err := exec.Command("find", homeDir, "-maxdepth", "2", "-type", "f", "-printf", "%T@\n").Output()
	if err != nil {
		return time.Time{}
	}

	var latest float64
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ts float64
		if _, err := fmt.Sscanf(line, "%f", &ts); err == nil && ts > latest {
			latest = ts
		}
	}

	if latest > 0 {
		return time.Unix(int64(latest), 0)
	}
	return time.Time{}
}

// collectInactiveUsers 仅返回超过指定天数未登录的用户。
func collectInactiveUsers(days int) []inactiveUser {
	all := collectAllUsers()
	var result []inactiveUser
	for _, u := range all {
		if u.daysSince >= days {
			result = append(result, u)
		}
	}
	return result
}

func resolveInactiveLastTime(now, lastLoginTime, createTime, homeMtime time.Time, hasLogin, hasProcess bool) time.Time {
	lastTime := lastLoginTime
	if !hasLogin {
		lastTime = createTime
	}

	if hasProcess {
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		if today.After(lastTime) {
			lastTime = today
		}
	}

	// 从未登录的用户的 home 目录文件可能是管理员创建的，不应视为用户活跃。
	if hasLogin && homeMtime.After(lastTime) {
		lastTime = homeMtime
	}

	return lastTime
}

func formatInactiveLastLogin(lastTime, createTime time.Time, hasLogin bool) string {
	if !hasLogin && !createTime.IsZero() && lastTime.Equal(createTime) {
		return createTime.Format("2006-01-02")
	}
	return lastTime.Format("2006-01-02")
}

// parseLastlog 解析 lastlog 命令输出，返回每个用户的最后登录时间。
func parseLastlog(userSet map[string]bool) map[string]time.Time {
	// LANG=C 强制英文输出，避免中文 locale 导致解析失败
	cmd := exec.Command("lastlog")
	cmd.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return map[string]time.Time{}
	}

	return parseLastlogOutput(string(out), userSet)
}

func parseLastlogOutput(output string, userSet map[string]bool) map[string]time.Time {
	result := make(map[string]time.Time)

	scanner := bufio.NewScanner(strings.NewReader(output))

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 1 {
			continue
		}

		username := fields[0]
		if !userSet[username] {
			continue
		}

		// "Never logged in" 或中文"从未登录"
		if strings.Contains(line, "Never logged in") || strings.Contains(line, "从未登录") {
			continue
		}

		// lastlog 日期部分的字段数不固定（取决于 IP/端口的长度）
		// 从末尾往前找到日期起始：最后一个是年份(4位数字)，往前依次是时间和日期
		// 格式1（无timezone）: Wed Apr 15 16:48:35 2026 → 5个字段
		// 格式2（有timezone）: Wed Apr 15 16:48:35 +0800 2026 → 6个字段
		// 策略：从末尾找到年份字段，往前取 5 或 6 个字段
		yearIdx := -1
		for i := len(fields) - 1; i >= 0; i-- {
			if len(fields[i]) == 4 {
				var year int
				if _, err := fmt.Sscanf(fields[i], "%d", &year); err == nil && year >= 2000 && year <= 2100 {
					yearIdx = i
					break
				}
			}
		}
		if yearIdx < 4 {
			continue // 不够字段来解析日期
		}

		// 检查年份前是否有 timezone（+HHMM / -HHMM 格式）
		dateStart := yearIdx - 4 // 至少: Weekday Month Day Time Year
		if dateStart > 1 && (strings.HasPrefix(fields[yearIdx-1], "+") || strings.HasPrefix(fields[yearIdx-1], "-")) && len(fields[yearIdx-1]) == 5 {
			dateStart = yearIdx - 5 // 含 timezone: Weekday Month Day Time TZ Year
		}

		if dateStart < 1 {
			continue
		}

		dateStr := strings.Join(fields[dateStart:yearIdx+1], " ")
		t, err := time.Parse("Mon Jan 2 15:04:05 2006", dateStr)
		if err != nil {
			t, err = time.Parse("Mon Jan _2 15:04:05 -0700 2006", dateStr)
			if err != nil {
				continue
			}
		}
		result[username] = t
	}

	return result
}

// getUserCreateTime 尝试获取用户创建时间作为从未登录用户的参考时间。
// 优先级：家目录 birth time > /etc/shadow 第三字段 > /etc/passwd 修改时间。
func getUserCreateTime(username string) time.Time {
	// 1. 家目录的 birth time（创建用户时 useradd -m 建立的精确时间）
	homeDir := filepath.Join("/home", username)
	if t := getDirBirthTime(homeDir); !t.IsZero() {
		return t
	}

	// 2. /etc/shadow 第三字段（上次密码修改日期，自 epoch 以来的天数）
	f, err := os.Open("/etc/shadow")
	if err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			parts := strings.Split(scanner.Text(), ":")
			if len(parts) < 3 || parts[0] != username {
				continue
			}
			var daysSinceEpoch int
			fmt.Sscanf(parts[2], "%d", &daysSinceEpoch)
			f.Close()
			if daysSinceEpoch > 0 {
				return time.Unix(int64(daysSinceEpoch)*86400, 0)
			}
			break
		}
		f.Close()
	}

	// 3. 兜底：/etc/passwd 修改时间
	if info, err := os.Stat("/etc/passwd"); err == nil {
		return info.ModTime()
	}

	return time.Now()
}

// getDirBirthTime 通过 stat 命令获取目录的 birth time（创建时间）。
// stat -c %W 输出自 epoch 以来的秒数，0 表示不可用。
func getDirBirthTime(dir string) time.Time {
	out, err := exec.Command("stat", "-c", "%W", dir).Output()
	if err != nil {
		return time.Time{}
	}
	var secs int64
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &secs)
	if secs <= 0 {
		return time.Time{}
	}
	return time.Unix(secs, 0)
}

// ── 注册 ──────────────────────────────────────────────────────────────────────

var userInactiveCmd = &cobra.Command{
	Use:   "inactive",
	Short: "长期不登录用户管理",
	Long: `检测和管理长期未登录的用户。

子命令：
  list              列出所有用户及其未登录天数
  warn              将不活跃用户警告写入 MOTD（需要 root）
  purge             删除超过指定天数未登录的用户（需要 root）
  monitor enable    启用每日自动检查（需要 root）
  monitor disable   禁用每日自动检查（需要 root）
  monitor status    查看定时任务状态

示例：
  server-mgr user inactive list
  sudo server-mgr user inactive warn --days 180
  sudo server-mgr user inactive purge --days 180
  sudo server-mgr user inactive monitor enable --days 180`,
}

func init() {
	// 三个 flag 的默认值统一取自 config.conf 的 INACTIVE_DAYS，
	// cron 里不带 --days 调用时拿到的就是管理员配置的阈值。
	days := config().InactiveDays

	// warn 命令的 flag
	userInactiveWarnCmd.Flags().IntVar(&inactiveWarnDays, "days", days,
		fmt.Sprintf("超过多少天未登录则写入警告（默认 %d）", days))

	// purge 命令的 flag
	userInactivePurgeCmd.Flags().IntVar(&inactivePurgeDaysFlag, "days", days,
		fmt.Sprintf("超过多少天未登录则删除（默认 %d）", days))

	// monitor enable 命令的 flag
	inactiveMonitorEnableCmd.Flags().IntVar(&inactiveMonitorDays, "days", days,
		fmt.Sprintf("不活跃阈值天数（默认 %d）", days))

	// 注册 monitor 子命令
	inactiveMonitorCmd.AddCommand(inactiveMonitorEnableCmd)
	inactiveMonitorCmd.AddCommand(inactiveMonitorDisableCmd)
	inactiveMonitorCmd.AddCommand(inactiveMonitorStatusCmd)

	userInactiveCmd.AddCommand(userInactiveListCmd, userInactiveWarnCmd, userInactivePurgeCmd, inactiveMonitorCmd)
	userCmd.AddCommand(userInactiveCmd)
}
