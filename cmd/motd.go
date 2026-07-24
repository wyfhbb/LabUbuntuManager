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
	motdDataDir      = serverMgrLibDir + "/motd"
	motdHeaderFile   = motdDataDir + "/header.txt"
	motdWarningsFile = motdDataDir + "/warnings.txt"
	motdDisabledList = motdDataDir + "/disabled-scripts.txt"
	motdUpdateDir    = "/etc/update-motd.d"
	motdScriptName   = "99-lab-info"
	motdScriptPath   = motdUpdateDir + "/" + motdScriptName
	bashRcPath       = "/etc/bash.bashrc"
	zshRcPath        = "/etc/zsh/zshrc"

	// 以下两项属于已废弃的公网 IP 链路，仅用于清理老机器上的残留。
	legacyMotdCronFile  = "/etc/cron.d/server-mgr-motd"
	legacyPublicIPCache = "/var/cache/server-mgr/public-ip.txt"
)

// ANSI 颜色常量
const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorCyan   = "\033[36m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorDim    = "\033[2m"
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

	// 成对标记界定注入片段的边界，删除时按标记区间精确移除。
	vscodeMotdBeginMarker = "# server-mgr vscode-motd begin"
	vscodeMotdEndMarker   = "# server-mgr vscode-motd end"
	// 老版本只写了单行标记（没有 end），删除时需要兼容。
	vscodeMotdLegacyMarker = "# server-mgr vscode-motd"
)

// vscodeMotdSnippet 直接调用 motd render，不走 run-parts：
// apt 升级 update-notifier 等包会恢复默认 MOTD 脚本的可执行位，
// 走 run-parts 会让 VSCode 终端里同时冒出 Ubuntu 默认 MOTD。
var vscodeMotdSnippet = vscodeMotdBeginMarker + "\n" +
	"if [ -n \"$VSCODE_IPC_HOOK_CLI\" ] || [ \"$TERM_PROGRAM\" = \"vscode\" ]; then\n" +
	"\t" + installedBinPath + " motd render 2>/dev/null\n" +
	"fi\n" +
	vscodeMotdEndMarker + "\n"

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
		for _, dir := range []string{motdDataDir, motdInitDir} {
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
		if err := ensureInstalled(); err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}

		// 6. 清理已废弃的公网 IP 链路（老机器上可能还留着每小时抓 IP 的 cron）
		cleanupLegacyPublicIP()

		// 7. 写入环境初始化/卸载脚本
		writeInitScript(installMiniforgePath, installMiniforgeScript, "Miniforge3 安装脚本")
		writeInitScript(uninstallMiniforgePath, uninstallMiniforgeScript, "Miniforge3 卸载脚本")
		writeInitScript(installUVPath, installUVScript, "uv 安装脚本")
		writeInitScript(uninstallUVPath, uninstallUVScript, "uv 卸载脚本")

		// 8. 注入 VSCode Remote SSH MOTD 显示
		injectVscodeMotd(bashRcPath)
		if zshInstalled() {
			injectVscodeMotd(zshRcPath)
		} else {
			fmt.Println("未检测到 zsh，跳过 zsh 注入")
		}

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

		// 2. 清理已废弃的公网 IP 链路残留
		cleanupLegacyPublicIP()

		// 3. 移除 VSCode MOTD 注入
		removeVscodeMotd(bashRcPath)
		removeVscodeMotd(zshRcPath)

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

// ── motd status ──────────────────────────────────────────────────────────────

var motdStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "检查自定义 MOTD 各环节是否到位",
	Run: func(cmd *cobra.Command, args []string) {
		if _, err := os.Stat(motdScriptPath); err == nil {
			fmt.Printf("自定义脚本:  已安装 %s\n", motdScriptPath)
		} else {
			fmt.Println("自定义脚本:  未安装")
			fmt.Println("启用方法:    sudo server-mgr motd set")
		}

		if _, err := os.Stat(installedBinPath); err == nil {
			fmt.Printf("二进制:      已安装 %s\n", installedBinPath)
		} else {
			fmt.Printf("二进制:      缺失 %s（MOTD 脚本将无输出）\n", installedBinPath)
		}

		if data, err := os.ReadFile(motdDisabledList); err == nil {
			count := 0
			for _, name := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if strings.TrimSpace(name) != "" {
					count++
				}
			}
			fmt.Printf("默认脚本:    已禁用 %d 个（记录: %s）\n", count, motdDisabledList)
		} else {
			fmt.Println("默认脚本:    未禁用")
		}

		fmt.Printf("bash 注入:   %s\n", vscodeMotdStatus(bashRcPath))
		if zshInstalled() {
			fmt.Printf("zsh 注入:    %s\n", vscodeMotdStatus(zshRcPath))
		} else {
			fmt.Println("zsh 注入:    跳过（未安装 zsh）")
		}

		if _, err := os.Stat(motdHeaderFile); err == nil {
			fmt.Printf("欢迎语:      %s\n", motdHeaderFile)
		} else {
			fmt.Println("欢迎语:      未设置")
		}

		if data, err := os.ReadFile(motdWarningsFile); err == nil && strings.TrimSpace(string(data)) != "" {
			fmt.Printf("MOTD 警告:   已写入 %s\n", motdWarningsFile)
		} else {
			fmt.Println("MOTD 警告:   无")
		}

		if _, err := os.Stat(legacyMotdCronFile); err == nil {
			fmt.Printf("\n提示: 发现已废弃的公网 IP 定时任务 %s，执行 sudo server-mgr motd set 可自动清理\n", legacyMotdCronFile)
		}
	},
}

// vscodeMotdStatus 描述某个 rc 文件的注入状态。
func vscodeMotdStatus(rcPath string) string {
	data, err := os.ReadFile(rcPath)
	if err != nil {
		return fmt.Sprintf("未注入（%s 不存在）", rcPath)
	}
	content := string(data)
	switch {
	case strings.Contains(content, vscodeMotdBeginMarker):
		return fmt.Sprintf("已注入 %s", rcPath)
	case strings.Contains(content, vscodeMotdLegacyMarker):
		return fmt.Sprintf("已注入旧版片段 %s（执行 sudo server-mgr motd set 可升级）", rcPath)
	default:
		return fmt.Sprintf("未注入 %s", rcPath)
	}
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

// zshInstalled 判断机器上是否装了 zsh。
// 没装 zsh 的机器上 /etc/zsh 不存在，直接写文件会失败并报错。
func zshInstalled() bool {
	if _, err := os.Stat(filepath.Dir(zshRcPath)); err == nil {
		return true
	}
	_, err := exec.LookPath("zsh")
	return err == nil
}

// injectVscodeMotd 向 shell 全局配置文件末尾写入 VSCode MOTD 代码片段。
// 已存在（含老版本片段）时先移除再写入，保证片段内容始终是最新的。
func injectVscodeMotd(rcPath string) {
	data, err := os.ReadFile(rcPath)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "错误: 无法读取 %s: %v\n", rcPath, err)
			return
		}
		// 文件不存在：只有目录已存在才创建，避免在没装对应 shell 的机器上凭空造目录
		if _, statErr := os.Stat(filepath.Dir(rcPath)); statErr != nil {
			fmt.Printf("跳过 VSCode MOTD 注入: %s 不存在\n", filepath.Dir(rcPath))
			return
		}
		if err := os.WriteFile(rcPath, []byte(vscodeMotdSnippet), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 无法写入 %s: %v\n", rcPath, err)
			return
		}
		fmt.Printf("VSCode MOTD 已注入: %s\n", rcPath)
		return
	}

	content, hadOld := stripVscodeMotd(string(data))
	content = strings.TrimRight(content, "\n") + "\n\n" + vscodeMotdSnippet
	if err := os.WriteFile(rcPath, []byte(content), rcFilePerm(rcPath)); err != nil {
		fmt.Fprintf(os.Stderr, "错误: 无法写入 %s: %v\n", rcPath, err)
		return
	}
	if hadOld {
		fmt.Printf("VSCode MOTD 已更新: %s\n", rcPath)
	} else {
		fmt.Printf("VSCode MOTD 已注入: %s\n", rcPath)
	}
}

// removeVscodeMotd 从 shell 全局配置文件中移除 VSCode MOTD 代码片段。
func removeVscodeMotd(rcPath string) {
	data, err := os.ReadFile(rcPath)
	if err != nil {
		return
	}
	content, changed := stripVscodeMotd(string(data))
	if !changed {
		return
	}
	if err := os.WriteFile(rcPath, []byte(content), rcFilePerm(rcPath)); err != nil {
		fmt.Fprintf(os.Stderr, "错误: 无法写入 %s: %v\n", rcPath, err)
		return
	}
	fmt.Printf("VSCode MOTD 已移除: %s\n", rcPath)
}

// rcFilePerm 返回 rc 文件的现有权限，读取失败时回落到 0644。
func rcFilePerm(rcPath string) os.FileMode {
	if info, err := os.Stat(rcPath); err == nil {
		return info.Mode().Perm()
	}
	return 0644
}

// stripVscodeMotd 从 rc 文件内容中删除已注入的片段，返回新内容与是否有改动。
//
// 按 begin/end 标记的区间删除；老版本只写了单行标记，回落到"标记行起、
// 到最近的独立 fi 行为止"的有限窗口。找不到结束边界时保持原样，
// 宁可留下片段也不要误删相邻的 shell 代码。
func stripVscodeMotd(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	changed := false

	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		paired := trimmed == vscodeMotdBeginMarker
		if !paired && trimmed != vscodeMotdLegacyMarker {
			out = append(out, lines[i])
			continue
		}

		end := findVscodeMotdBlockEnd(lines, i, paired)
		if end < 0 {
			out = append(out, lines[i])
			continue
		}
		// 注入时片段前带一个空行，一并回收，避免反复 set/reset 堆积空行
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
		i = end
		changed = true
	}

	if !changed {
		return content, false
	}
	return strings.Join(out, "\n"), true
}

// findVscodeMotdBlockEnd 返回片段最后一行的下标，找不到返回 -1。
func findVscodeMotdBlockEnd(lines []string, start int, paired bool) int {
	if paired {
		for i := start + 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == vscodeMotdEndMarker {
				return i
			}
		}
		return -1
	}

	// 老片段固定为「标记 / if / 命令 / fi」四行，超出窗口就认为不是它
	const legacyMaxLines = 5
	for i := start + 1; i < len(lines) && i <= start+legacyMaxLines; i++ {
		if strings.TrimSpace(lines[i]) == "fi" {
			return i
		}
	}
	return -1
}

// cleanupLegacyPublicIP 清理已废弃的公网 IP 缓存链路（每小时 curl 的 cron 与缓存文件）。
// MOTD 从未读取过该缓存，保留只会每小时无谓地访问外网。
func cleanupLegacyPublicIP() {
	if err := os.Remove(legacyMotdCronFile); err == nil {
		fmt.Printf("已清理废弃的公网 IP 定时任务: %s\n", legacyMotdCronFile)
	}
	os.Remove(legacyPublicIPCache)
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
	if localIP := getLocalIP(); localIP != "" {
		fmt.Println("  " + colorDim + "局域网" + colorReset + " " + localIP)
	}
}

func renderMotdDisks() {
	provider := NewProcMountDiskUsageProvider()
	usages, err := provider.ListDiskUsage()
	if err != nil || len(usages) == 0 {
		return
	}

	warnPercent := config().DiskWarnPercent
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, u := range usages {
		warn := ""
		percent := fmt.Sprintf("(%.1f%%)", u.UsedPercent)
		if u.UsedPercent > warnPercent {
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

// getLocalIP 通过 ip route get 获取本机出口 IP。
func getLocalIP() string {
	out, err := exec.Command("ip", "route", "get", "1.1.1.1").Output()
	if err != nil {
		return ""
	}
	// 典型输出: "1.1.1.1 via 10.0.0.1 dev eth0 src 192.168.1.100 uid 1000"
	line := string(out)
	if idx := strings.Index(line, "src "); idx >= 0 {
		if f := strings.Fields(line[idx+4:]); len(f) > 0 {
			return f[0]
		}
	}
	return ""
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

func init() {
	motdCmd.AddCommand(motdSetCmd)
	motdCmd.AddCommand(motdResetCmd)
	motdCmd.AddCommand(motdShowCmd)
	motdCmd.AddCommand(motdStatusCmd)
	motdCmd.AddCommand(motdRenderCmd)

	rootCmd.AddCommand(motdCmd)
}
