package cmd

import (
	"bufio"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

const (
	motdDataDir      = "/usr/local/lib/server-mgr/motd"
	motdHeaderFile   = motdDataDir + "/header.txt"
	motdWarningsFile = motdDataDir + "/warnings.txt"
	motdDisabledList = motdDataDir + "/disabled-scripts.txt"
	motdUpdateDir    = "/etc/update-motd.d"
	motdScriptName   = "99-lab-info"
	motdScriptPath   = motdUpdateDir + "/" + motdScriptName
	motdCronFile     = "/etc/cron.d/server-mgr-motd"
	publicIPCache    = "/var/cache/server-mgr/public-ip.txt"
)

// ANSI 颜色常量
const (
	colorReset   = "\033[0m"
	colorBold    = "\033[1m"
	colorCyan    = "\033[36m"
	colorGreen   = "\033[32m"
	colorYellow  = "\033[33m"
	colorRed     = "\033[31m"
	colorDim     = "\033[2m"
)

//go:embed shell/install-miniforge.sh
var installMiniforgeScript string

//go:embed shell/uninstall-miniforge.sh
var uninstallMiniforgeScript string

const (
	motdInitDir            = motdDataDir + "/init"
	installMiniforgePath   = motdInitDir + "/install-miniforge.sh"
	uninstallMiniforgePath = motdInitDir + "/uninstall-miniforge.sh"
	installUVPath          = motdInitDir + "/install-uv.sh"
	uninstallUVPath        = motdInitDir + "/uninstall-uv.sh"
	vscodeMotdMarker       = "# server-mgr vscode-motd"
)

var vscodeMotdSnippet = vscodeMotdMarker + "\n" +
	"if [ -n \"$VSCODE_IPC_HOOK_CLI\" ] || [ \"$TERM_PROGRAM\" = \"vscode\" ]; then\n" +
	"\trun-parts /etc/update-motd.d/ 2>/dev/null\n" +
	"fi\n"

var installUVScript = "#!/bin/bash\n" +
	"# uv 安装脚本（由 server-mgr motd set 自动生成）\n" +
	"# 使用方法：bash " + installUVPath + "\n" +
	"set -e\n" +
	"echo \"正在安装 uv...\"\n" +
	"curl -LsSf https://astral.sh/uv/install.sh | sh\n" +
	"echo \"\"\n" +
	"echo \"uv 安装完成！\"\n" +
	"echo \"请重启终端或运行: source ~/.local/bin/env\"\n"

var uninstallUVScript = "#!/bin/bash\n" +
	"# uv 卸载脚本（由 server-mgr motd set 自动生成）\n" +
	"# 使用方法：bash " + uninstallUVPath + "\n" +
	"set -e\n" +
	"echo \"正在卸载 uv...\"\n" +
	"rm -f ~/.local/bin/uv ~/.local/bin/uvx\n" +
	"echo \"uv 已卸载完成\"\n"

var motdScriptContent = "#!/bin/bash\n" +
	"# server-mgr MOTD 脚本，由 server-mgr motd set 自动生成\n" +
	installedBinPath + " motd render 2>/dev/null\n"

var motdIPCronContent = "# server-mgr 公网 IP 定时缓存（每小时）\n" +
	"# 由 server-mgr motd set 自动生成，请勿手动编辑\n" +
	"0 * * * * root curl -s --connect-timeout 5 --max-time 10 ifconfig.me/ip > " + publicIPCache + " 2>/dev/null\n"

var defaultMotdHeader = "欢迎使用实验室服务器！如需帮助请联系管理员。"

// ── motd 命令组 ──────────────────────────────────────────────────────────────

var motdCmd = &cobra.Command{
	Use:   "motd",
	Short: "管理登录欢迎信息（MOTD）",
}

// ── motd set ─────────────────────────────────────────────────────────────────

var motdSetCmd = &cobra.Command{
	Use:   "set",
	Short: "启用实验室自定义 MOTD（需要 root）",
	Run: func(cmd *cobra.Command, args []string) {
		if os.Getuid() != 0 {
			fmt.Fprintln(os.Stderr, "错误: 此命令需要 root 权限，请使用 sudo 执行")
			os.Exit(1)
		}

		// 1. 创建目录
		for _, dir := range []string{motdDataDir, motdInitDir, "/var/cache/server-mgr"} {
			if err := os.MkdirAll(dir, 0755); err != nil {
				fmt.Fprintf(os.Stderr, "错误: 无法创建目录 %s: %v\n", dir, err)
				os.Exit(1)
			}
		}

		// 2. 写入默认 header（不覆盖已有）
		if _, err := os.Stat(motdHeaderFile); os.IsNotExist(err) {
			if err := os.WriteFile(motdHeaderFile, []byte(defaultMotdHeader+"\n"), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "错误: 无法写入 %s: %v\n", motdHeaderFile, err)
				os.Exit(1)
			}
			fmt.Printf("默认欢迎语已写入: %s\n", motdHeaderFile)
		}

		// 3. 禁用系统默认 MOTD 脚本
		disabled := disableDefaultMotdScripts()
		if disabled > 0 {
			fmt.Printf("已禁用 %d 个系统默认 MOTD 脚本\n", disabled)
		}

		// 4. 写入自定义 MOTD 脚本
		if err := os.WriteFile(motdScriptPath, []byte(motdScriptContent), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 无法写入 %s: %v\n", motdScriptPath, err)
			os.Exit(1)
		}
		fmt.Printf("MOTD 脚本已安装: %s\n", motdScriptPath)

		// 5. 安装二进制到系统路径
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

		// 6. 设置公网 IP 缓存 cron
		if err := os.WriteFile(motdCronFile, []byte(motdIPCronContent), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 无法写入 cron 配置 %s: %v\n", motdCronFile, err)
			os.Exit(1)
		}
		fmt.Printf("公网 IP 缓存已配置: %s\n", motdCronFile)

		// 7. 立即获取一次公网 IP
		fmt.Print("正在获取公网 IP...")
		if fetchPublicIP() {
			fmt.Println(" 完成")
		} else {
			fmt.Println(" 跳过（无法连接外部网络）")
		}

		// 8. 写入环境初始化/卸载脚本
		writeInitScript(installMiniforgePath, installMiniforgeScript, "Miniforge3 安装脚本")
		writeInitScript(uninstallMiniforgePath, uninstallMiniforgeScript, "Miniforge3 卸载脚本")
		writeInitScript(installUVPath, installUVScript, "uv 安装脚本")
		writeInitScript(uninstallUVPath, uninstallUVScript, "uv 卸载脚本")

		// 9. 注入 VSCode Remote SSH MOTD 显示
		injectVscodeMotd("/etc/bash.bashrc")
		injectVscodeMotd("/etc/zsh/zshrc")

		fmt.Println()
		fmt.Println("MOTD 已启用。用户登录时将看到自定义欢迎信息。")
		fmt.Printf("编辑欢迎语: %s\n", motdHeaderFile)
		fmt.Println("预览效果:   server-mgr motd show")
		fmt.Println()
		fmt.Println("环境初始化脚本已生成，用户可执行：")
		fmt.Printf("  bash %s\n", installMiniforgePath)
		fmt.Printf("  bash %s\n", installUVPath)
	},
}

// ── motd reset ───────────────────────────────────────────────────────────────

var motdResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "恢复系统默认 MOTD（需要 root）",
	Run: func(cmd *cobra.Command, args []string) {
		if os.Getuid() != 0 {
			fmt.Fprintln(os.Stderr, "错误: 此命令需要 root 权限，请使用 sudo 执行")
			os.Exit(1)
		}

		// 1. 删除自定义脚本
		os.Remove(motdScriptPath)

		// 2. 删除 cron
		os.Remove(motdCronFile)

		// 3. 移除 VSCode MOTD 注入
		removeVscodeMotd("/etc/bash.bashrc")
		removeVscodeMotd("/etc/zsh/zshrc")

		// 4. 恢复之前被禁用的默认脚本
		restored := enableDefaultMotdScripts()
		if restored > 0 {
			fmt.Printf("已恢复 %d 个系统默认 MOTD 脚本\n", restored)
		}

		fmt.Println("已恢复系统默认 MOTD")
		fmt.Printf("自定义数据保留在 %s，如需清理请手动删除\n", motdDataDir)
	},
}

// ── motd show ────────────────────────────────────────────────────────────────

var motdShowCmd = &cobra.Command{
	Use:   "show",
	Short: "预览当前 MOTD 输出",
	Run: func(cmd *cobra.Command, args []string) {
		renderMotd()
	},
}

// ── motd render（供 MOTD 脚本调用，不直接面向用户）─────────────────────────────

var motdRenderCmd = &cobra.Command{
	Use:    "render",
	Short:  "渲染 MOTD 内容（供系统调用）",
	Hidden: true,
	Run: func(cmd *cobra.Command, args []string) {
		renderMotd()
	},
}

func writeInitScript(path, content, desc string) {
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "错误: 无法写入 %s: %v\n", path, err)
		os.Exit(1)
	}
	fmt.Printf("%s已写入: %s\n", desc, path)
}

// injectVscodeMotd 向 shell 全局配置文件末尾追加 VSCode MOTD 代码片段（跳过已有）。
func injectVscodeMotd(rcPath string) {
	data, err := os.ReadFile(rcPath)
	if err != nil {
		// 文件不存在则创建
		if err := os.WriteFile(rcPath, []byte("\n"+vscodeMotdSnippet), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 无法写入 %s: %v\n", rcPath, err)
			return
		}
		fmt.Printf("VSCode MOTD 已注入: %s\n", rcPath)
		return
	}
	if strings.Contains(string(data), vscodeMotdMarker) {
		fmt.Printf("VSCode MOTD 已存在，跳过: %s\n", rcPath)
		return
	}
	f, err := os.OpenFile(rcPath, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: 无法追加 %s: %v\n", rcPath, err)
		return
	}
	defer f.Close()
	f.WriteString("\n" + vscodeMotdSnippet)
	fmt.Printf("VSCode MOTD 已注入: %s\n", rcPath)
}

// removeVscodeMotd 从 shell 全局配置文件中移除 VSCode MOTD 代码片段。
func removeVscodeMotd(rcPath string) {
	data, err := os.ReadFile(rcPath)
	if err != nil {
		return
	}
	if !strings.Contains(string(data), vscodeMotdMarker) {
		return
	}
	lines := strings.Split(string(data), "\n")
	var filtered []string
	skip := false
	for _, line := range lines {
		if strings.Contains(line, vscodeMotdMarker) {
			skip = true
			continue
		}
		if skip && (strings.HasPrefix(line, "if [") || strings.HasPrefix(line, "\trun-parts") || strings.HasPrefix(line, "fi")) {
			continue
		}
		if skip && line == "" {
			skip = false
			continue
		}
		skip = false
		filtered = append(filtered, line)
	}
	os.WriteFile(rcPath, []byte(strings.Join(filtered, "\n")), 0644)
	fmt.Printf("VSCode MOTD 已移除: %s\n", rcPath)
}

// ── 渲染逻辑 ─────────────────────────────────────────────────────────────────

func renderMotd() {
	// Header（用户自定义欢迎语）
	if data, err := os.ReadFile(motdHeaderFile); err == nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			fmt.Println(colorBold + s + colorReset)
		}
	}
	fmt.Println()

	// 系统信息：hostname · OS · uptime
	renderSystemInfo()

	// 网络信息：本机 · 网关 · 公网
	renderNetworkInfo()

	fmt.Println()

	// 磁盘用量
	renderMotdDisks()

	// 环境初始化脚本提示
	renderInitHints()

	// 系统提醒（apt 更新、需要重启）
	renderSystemWarnings()

	// 自定义警告（user inactive 等写入）
	if data, err := os.ReadFile(motdWarningsFile); err == nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			fmt.Println()
			fmt.Println(s)
		}
	}
}

func renderSystemInfo() {
	var parts []string

	if hostname, err := os.Hostname(); err == nil {
		parts = append(parts, colorCyan+hostname+colorReset)
	}

	if name := readOSReleasePrettyName(); name != "" {
		parts = append(parts, colorGreen+name+colorReset)
	}

	if up := formatUptime(); up != "" {
		parts = append(parts, colorDim+"Up "+up+colorReset)
	}

	if len(parts) > 0 {
		fmt.Println("  " + strings.Join(parts, " · "))
	}
}

func renderNetworkInfo() {
	localIP, _ := getLocalIPAndGateway()
	if localIP != "" {
		fmt.Println("  " + colorDim + "局域网" + colorReset + " " + localIP)
	}
}

func renderMotdDisks() {
	provider := NewProcMountDiskUsageProvider()
	usages, err := provider.ListDiskUsage()
	if err != nil || len(usages) == 0 {
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, u := range usages {
		warn := ""
		percent := fmt.Sprintf("(%.1f%%)", u.UsedPercent)
		if u.UsedPercent > defaultDiskUsageWarnPercent {
			warn = colorRed + " [!]" + colorReset
			percent = colorRed + percent + colorReset
		}
		fmt.Fprintf(tw, "  %s\t%s / %s\t%s%s\n",
			u.MountPoint,
			formatCapacityByGB(u.UsedGB),
			formatCapacityByGB(u.TotalGB),
			percent,
			warn,
		)
	}
	tw.Flush()
}

func renderInitHints() {
	var hints []string

	if _, err := os.Stat(installMiniforgePath); err == nil {
		hints = append(hints, "bash "+installMiniforgePath+"  "+colorDim+"# 安装 Miniforge3 (conda)"+colorReset)
	}
	if _, err := os.Stat(installUVPath); err == nil {
		hints = append(hints, "bash "+installUVPath+"  "+colorDim+"# 安装 uv (Python 包管理)"+colorReset)
	}
	if _, err := os.Stat(uninstallMiniforgePath); err == nil {
		hints = append(hints, "bash "+uninstallMiniforgePath+"  "+colorDim+"# 卸载 Miniforge3"+colorReset)
	}
	if _, err := os.Stat(uninstallUVPath); err == nil {
		hints = append(hints, "bash "+uninstallUVPath+"  "+colorDim+"# 卸载 uv"+colorReset)
	}

	if len(hints) > 0 {
		fmt.Println()
		fmt.Println("  " + colorBold + "环境初始化脚本：" + colorReset)
		for _, h := range hints {
			fmt.Println("    " + h)
		}
	}
}

func isESMLine(line string) bool {
	// 过滤掉 ESM（Extended Security Maintenance）相关提示行
	esmKeywords := []string{"ESM", "esm", "扩展安全维护", "ubuntu.com/esm", "ubuntu.com/advantage"}
	for _, kw := range esmKeywords {
		if strings.Contains(line, kw) {
			return true
		}
	}
	return false
}

func renderSystemWarnings() {
	var warnings []string

	// apt 可用更新
	if data, err := os.ReadFile("/var/lib/update-notifier/updates-available"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !isESMLine(line) {
				warnings = append(warnings, colorYellow+line+colorReset)
			}
		}
	}

	// 需要重启
	if _, err := os.Stat("/var/run/reboot-required"); err == nil {
		warnings = append(warnings, colorRed+colorBold+"*** 系统需要重启 ***"+colorReset)
	}

	if len(warnings) > 0 {
		fmt.Println()
		for _, w := range warnings {
			fmt.Println("  " + w)
		}
	}
}

// ── 辅助函数 ─────────────────────────────────────────────────────────────────

func readOSReleasePrettyName() string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			val := strings.TrimPrefix(line, "PRETTY_NAME=")
			return strings.Trim(val, "\"")
		}
	}
	return ""
}

func formatUptime() string {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return ""
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return ""
	}

	totalSecs := int(secs)
	days := totalSecs / 86400
	hours := (totalSecs % 86400) / 3600

	if days > 0 {
		return fmt.Sprintf("%d 天 %d 小时", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%d 小时", hours)
	}
	return fmt.Sprintf("%d 分钟", (totalSecs%3600)/60)
}

// getLocalIPAndGateway 通过 ip route get 获取本机出口 IP 和默认网关。
func getLocalIPAndGateway() (localIP, gateway string) {
	out, err := exec.Command("ip", "route", "get", "1.1.1.1").Output()
	if err != nil {
		return "", ""
	}
	// 典型输出: "1.1.1.1 via 10.0.0.1 dev eth0 src 192.168.1.100 uid 1000"
	line := string(out)

	if idx := strings.Index(line, "via "); idx >= 0 {
		rest := line[idx+4:]
		if f := strings.Fields(rest); len(f) > 0 {
			gateway = f[0]
		}
	}
	if idx := strings.Index(line, "src "); idx >= 0 {
		rest := line[idx+4:]
		if f := strings.Fields(rest); len(f) > 0 {
			localIP = f[0]
		}
	}
	return
}

// disableDefaultMotdScripts 将 /etc/update-motd.d/ 下的默认脚本去掉可执行权限，
// 并记录被禁用的文件名以便 reset 时精确恢复。
func disableDefaultMotdScripts() int {
	entries, err := os.ReadDir(motdUpdateDir)
	if err != nil {
		return 0
	}

	var disabled []string
	for _, entry := range entries {
		if entry.Name() == motdScriptName {
			continue
		}
		path := filepath.Join(motdUpdateDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.Mode()&0111 != 0 {
			os.Chmod(path, info.Mode()&^0111)
			disabled = append(disabled, entry.Name())
		}
	}

	if len(disabled) > 0 {
		os.WriteFile(motdDisabledList, []byte(strings.Join(disabled, "\n")+"\n"), 0644)
	}
	return len(disabled)
}

// enableDefaultMotdScripts 根据之前保存的列表，恢复被禁用脚本的可执行权限。
func enableDefaultMotdScripts() int {
	data, err := os.ReadFile(motdDisabledList)
	if err != nil {
		return 0
	}

	count := 0
	for _, name := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if name == "" {
			continue
		}
		path := filepath.Join(motdUpdateDir, name)
		if info, err := os.Stat(path); err == nil {
			os.Chmod(path, info.Mode()|0111)
			count++
		}
	}

	os.Remove(motdDisabledList)
	return count
}

// fetchPublicIP 通过外部服务获取公网 IP 并写入缓存文件。
func fetchPublicIP() bool {
	out, err := exec.Command("curl", "-s", "--connect-timeout", "5", "--max-time", "10", "ifconfig.me/ip").Output()
	if err != nil {
		out, err = exec.Command("wget", "-qO-", "--timeout=10", "ifconfig.me/ip").Output()
		if err != nil {
			return false
		}
	}
	ip := strings.TrimSpace(string(out))
	if ip == "" {
		return false
	}
	// 简单校验：公网 IP 不应包含 HTML 标签或换行
	if strings.Contains(ip, "<") || strings.Contains(ip, "\n") {
		return false
	}
	return os.WriteFile(publicIPCache, []byte(ip), 0644) == nil
}

func init() {
	motdCmd.AddCommand(motdSetCmd)
	motdCmd.AddCommand(motdResetCmd)
	motdCmd.AddCommand(motdShowCmd)
	motdCmd.AddCommand(motdRenderCmd)

	rootCmd.AddCommand(motdCmd)
}
