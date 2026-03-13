package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// 不作为数据盘候选的挂载点（精确匹配）
var systemMountExact = map[string]struct{}{
	"/":         {},
	"/boot":     {},
	"/boot/efi": {},
	"/home":     {},
}

// 不作为数据盘候选的挂载点前缀
var systemMountPrefixes = []string{
	"/proc", "/sys", "/dev", "/run", "/tmp", "/snap",
}

// dataMountCandidates 从已有挂载列表中筛选出可作为用户数据目录的挂载点。
// 排除系统盘和常见伪文件系统，保留真实数据盘（如 /workspace, /data, /mnt/xxx）。
func dataMountCandidates(usages []DiskUsage) []DiskUsage {
	var result []DiskUsage
	for _, u := range usages {
		if _, excluded := systemMountExact[u.MountPoint]; excluded {
			continue
		}
		skip := false
		for _, prefix := range systemMountPrefixes {
			if strings.HasPrefix(u.MountPoint, prefix) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		result = append(result, u)
	}
	return result
}

// ── user list ────────────────────────────────────────────────────────────────

var userListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出所有普通用户及其数据目录映射",
	Run: func(cmd *cobra.Command, args []string) {
		// 获取数据盘挂载点，用于检测每个用户在各盘的目录
		var dataMounts []DiskUsage
		if provider := NewProcMountDiskUsageProvider(); provider != nil {
			if all, err := provider.ListDiskUsage(); err == nil {
				dataMounts = dataMountCandidates(all)
			}
		}

		f, err := os.Open("/etc/passwd")
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: 读取 /etc/passwd 失败: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()

		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "用户名\tUID\t全名\t主目录\t数据目录")

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
			username, uid, gecos, homeDir, shell := parts[0], parts[2], parts[4], parts[5], parts[6]
			var uidNum int
			fmt.Sscanf(uid, "%d", &uidNum)
			if uidNum < 1000 {
				continue
			}
			if shell == "/usr/sbin/nologin" || shell == "/bin/false" {
				continue
			}
			fullName := strings.SplitN(gecos, ",", 2)[0]

			// 检测该用户在各数据盘上实际存在的目录
			var dirs []string
			for _, m := range dataMounts {
				dir := filepath.Join(m.MountPoint, username)
				if _, err := os.Stat(dir); err == nil {
					dirs = append(dirs, dir)
				}
			}
			dataDirsStr := strings.Join(dirs, ", ")
			if dataDirsStr == "" {
				dataDirsStr = "-"
			}

			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", username, uid, fullName, homeDir, dataDirsStr)
		}
		tw.Flush()
	},
}

// ── user add ─────────────────────────────────────────────────────────────────

var userAddCmd = &cobra.Command{
	Use:   "add <用户名>",
	Short: "创建新用户，并在数据盘上建立工作目录",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if os.Getuid() != 0 {
			fmt.Fprintln(os.Stderr, "错误: 请使用 sudo 运行")
			os.Exit(1)
		}

		username := args[0]

		// 校验用户名格式
		for _, c := range username {
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
				fmt.Fprintln(os.Stderr, "错误: 用户名只能包含小写字母、数字、下划线和连字符")
				os.Exit(1)
			}
		}

		// 检查用户是否已存在
		if _, err := exec.Command("id", username).Output(); err == nil {
			fmt.Fprintf(os.Stderr, "错误: 用户 '%s' 已存在\n", username)
			os.Exit(1)
		}

		// 读取全名（写入 /etc/passwd GECOS 字段，供磁盘统计等脚本使用）
		reader := bufio.NewReader(os.Stdin)
		var fullName string
		for {
			fmt.Print("请输入用户全名: ")
			line, _ := reader.ReadString('\n')
			fullName = strings.TrimSpace(line)
			if fullName != "" {
				break
			}
			fmt.Println("  全名不能为空，请重新输入")
		}

		// 获取数据盘候选列表
		provider := NewProcMountDiskUsageProvider()
		allUsages, err := provider.ListDiskUsage()
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: 查询磁盘失败: %v\n", err)
			os.Exit(1)
		}
		candidates := dataMountCandidates(allUsages)
		if len(candidates) == 0 {
			fmt.Fprintln(os.Stderr, "错误: 未找到可用的数据盘挂载点（非系统盘）")
			os.Exit(1)
		}

		homeDir := filepath.Join("/home", username)

		// 确认：列出所有将要操作的路径
		fmt.Printf("即将执行：\n")
		fmt.Printf("  创建用户：%s\n", username)
		fmt.Printf("  家目录：  %s\n", homeDir)
		for _, c := range candidates {
			workDir := filepath.Join(c.MountPoint, username)
			linkPath := filepath.Join(homeDir, filepath.Base(c.MountPoint))
			fmt.Printf("  工作目录：%s  (剩余 %s)\n", workDir, formatCapacityByGB(c.FreeGB))
			fmt.Printf("  符号链接：%s -> %s\n", linkPath, workDir)
		}

		fmt.Print("\n确认创建? (y/N): ")
		confirm, _ := reader.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(confirm)) != "y" {
			fmt.Println("已取消")
			return
		}

		// 1. 创建用户（-m 自动创建家目录，-s 指定 shell）
		fmt.Print("正在创建用户... ")
		if out, err := exec.Command("useradd", "-m", "-s", "/bin/bash", "-c", fullName, username).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "\n错误: useradd 失败: %v\n%s\n", err, out)
			os.Exit(1)
		}
		fmt.Println("完成")

		// 2. 在每块数据盘上创建工作目录并建立符号链接
		for _, c := range candidates {
			workDir := filepath.Join(c.MountPoint, username)
			linkPath := filepath.Join(homeDir, filepath.Base(c.MountPoint))

			fmt.Printf("正在创建工作目录 %s ... ", workDir)
			if err := os.MkdirAll(workDir, 0700); err != nil {
				fmt.Fprintf(os.Stderr, "\n错误: 创建目录 %s 失败: %v\n", workDir, err)
				os.Exit(1)
			}
			if out, err := exec.Command("chown", username+":"+username, workDir).CombinedOutput(); err != nil {
				fmt.Fprintf(os.Stderr, "\n错误: chown 失败: %v\n%s\n", err, out)
				os.Exit(1)
			}
			fmt.Println("完成")

			fmt.Printf("正在创建符号链接 %s ... ", linkPath)
			if err := os.Symlink(workDir, linkPath); err != nil {
				fmt.Fprintf(os.Stderr, "\n错误: 创建符号链接失败: %v\n", err)
				os.Exit(1)
			}
			if out, err := exec.Command("chown", "-h", username+":"+username, linkPath).CombinedOutput(); err != nil {
				fmt.Fprintf(os.Stderr, "\n错误: 链接 chown 失败: %v\n%s\n", err, out)
				os.Exit(1)
			}
			fmt.Println("完成")
		}

		// 3. 强制首次登录修改密码
		fmt.Print("正在设置密码（首次登录须修改）... ")
		if out, err := exec.Command("chage", "-d", "0", username).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "\n错误: chage 失败: %v\n%s\n", err, out)
			os.Exit(1)
		}
		passwdCmd := exec.Command("passwd", username)
		passwdCmd.Stdin = os.Stdin
		passwdCmd.Stdout = os.Stdout
		passwdCmd.Stderr = os.Stderr
		if err := passwdCmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 设置密码失败: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("\n用户 %s 创建成功\n", username)
		fmt.Printf("  家目录：%s\n", homeDir)
		for _, c := range candidates {
			workDir := filepath.Join(c.MountPoint, username)
			linkPath := filepath.Join(homeDir, filepath.Base(c.MountPoint))
			fmt.Printf("  %s -> %s\n", linkPath, workDir)
		}
	},
}

// ── user del ─────────────────────────────────────────────────────────────────

var userDelPurge bool

var userDelCmd = &cobra.Command{
	Use:   "del <用户名>",
	Short: "删除用户，--purge 同时删除家目录及各数据盘目录",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if os.Getuid() != 0 {
			fmt.Fprintln(os.Stderr, "错误: 请使用 sudo 运行")
			os.Exit(1)
		}
		username := args[0]

		// 删除前先收集数据盘目录，避免 userdel 之后 id 查不到用户
		var dataDirs []string
		if userDelPurge {
			provider := NewProcMountDiskUsageProvider()
			if allUsages, err := provider.ListDiskUsage(); err == nil {
				for _, c := range dataMountCandidates(allUsages) {
					dir := filepath.Join(c.MountPoint, username)
					if _, err := os.Stat(dir); err == nil {
						dataDirs = append(dataDirs, dir)
					}
				}
			}
		}

		// 删除系统用户（-r 一并删家目录）
		delArgs := []string{username}
		if userDelPurge {
			delArgs = []string{"-r", username}
		}
		c := exec.Command("userdel", delArgs...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "错误: userdel 失败: %v\n", err)
			os.Exit(1)
		}

		// 清理各数据盘上的用户目录
		for _, dir := range dataDirs {
			fmt.Printf("正在删除 %s ... ", dir)
			if err := os.RemoveAll(dir); err != nil {
				fmt.Fprintf(os.Stderr, "\n错误: 删除 %s 失败: %v\n", dir, err)
				os.Exit(1)
			}
			fmt.Println("完成")
		}
	},
}

// ── user passwd ───────────────────────────────────────────────────────────────

var userPasswdCmd = &cobra.Command{
	Use:   "passwd <用户名>",
	Short: "修改用户密码",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if os.Getuid() != 0 {
			fmt.Fprintln(os.Stderr, "错误: 请使用 sudo 运行")
			os.Exit(1)
		}
		c := exec.Command("passwd", args[0])
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "错误: passwd 失败: %v\n", err)
			os.Exit(1)
		}
	},
}

// ── 注册 ──────────────────────────────────────────────────────────────────────

var userCmd = &cobra.Command{
	Use:   "user",
	Short: "用户管理",
}

func init() {
	userDelCmd.Flags().BoolVar(&userDelPurge, "purge", false, "同时删除家目录")
	userCmd.AddCommand(userListCmd, userAddCmd, userDelCmd, userPasswdCmd)
	rootCmd.AddCommand(userCmd)
}
