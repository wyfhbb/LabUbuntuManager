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

// readHidden 关闭终端回显读取一行（用于密码输入）。
// 非 TTY（如测试用管道喂 stdin）时 stty 会失败，此时自动降级为明文读取，不影响功能。
func readHidden(reader *bufio.Reader, prompt string) string {
	fmt.Print(prompt)
	echoOff := exec.Command("stty", "-echo")
	echoOff.Stdin = os.Stdin
	hidden := echoOff.Run() == nil
	line, _ := reader.ReadString('\n')
	if hidden {
		echoOn := exec.Command("stty", "echo")
		echoOn.Stdin = os.Stdin
		echoOn.Run()
		fmt.Println()
	}
	return strings.TrimSpace(line)
}

// rollbackUserAdd 在 user add 中途失败时逆序清理本次真正创建的资源：
// 先删各数据盘工作目录（逆序），再删用户及家目录。删除动作经 removeDir / delUser
// 注入以便测试。返回未能清理的残留说明（含手动清理命令），为空表示已全部清理干净。
func rollbackUserAdd(username string, workDirs []string, userCreated bool,
	removeDir func(string) error, delUser func(string) error) []string {
	var residues []string
	for i := len(workDirs) - 1; i >= 0; i-- {
		if err := removeDir(workDirs[i]); err != nil {
			residues = append(residues,
				fmt.Sprintf("目录 %s 未能删除（%v），请手动执行: rm -rf %s", workDirs[i], err, workDirs[i]))
		}
	}
	if userCreated {
		if err := delUser(username); err != nil {
			residues = append(residues,
				fmt.Sprintf("用户 %s 未能删除（%v），请手动执行: userdel -r %s", username, err, username))
		}
	}
	return residues
}

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

		homeDir := filepath.Join("/home", username)

		// 家目录不应预先存在：存在则可能是同名用户的残留。回滚会用 userdel -r 删家目录，
		// 绝不能因此删掉不是本次创建的目录 —— 在动手前就停下来报错
		if _, err := os.Lstat(homeDir); err == nil {
			fmt.Fprintf(os.Stderr, "错误: 家目录 %s 已存在，中止创建（不会改动已存在的目录）\n", homeDir)
			os.Exit(1)
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

		// 各数据盘上的工作目录同样不应预先存在：先整体预检一遍，任一存在就停下来，
		// 既避免创建到一半才发现，也避免回滚阶段误删已存在的同名目录
		for _, c := range candidates {
			workDir := filepath.Join(c.MountPoint, username)
			if _, err := os.Lstat(workDir); err == nil {
				fmt.Fprintf(os.Stderr, "错误: 工作目录 %s 已存在，中止创建（不会改动已存在的目录）\n", workDir)
				os.Exit(1)
			}
		}

		reader := bufio.NewReader(os.Stdin)

		// 读取全名（写入 /etc/passwd GECOS 字段，供磁盘统计等脚本使用）
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

		// 读取初始密码：两次一致即可，允许弱密码。两次不一致只是重新来过，此时尚未触碰系统
		var password string
		for {
			p1 := readHidden(reader, "请输入初始密码: ")
			if p1 == "" {
				fmt.Println("  密码不能为空，请重新输入")
				continue
			}
			p2 := readHidden(reader, "请再次输入密码: ")
			if p1 != p2 {
				fmt.Println("  两次输入不一致，请重新输入")
				continue
			}
			password = p1
			break
		}

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

		// ── 提交阶段 ──
		// 所有输入已在上面校验完毕，这里才开始改系统。任一步失败即回滚本次已创建的内容；
		// 回滚本身若再失败，明确列出残留路径和手动清理命令，绝不静默吞掉。
		var createdWorkDirs []string
		userCreated := false

		abort := func(format string, a ...any) {
			fmt.Fprintf(os.Stderr, "\n"+format+"\n", a...)
			residues := rollbackUserAdd(username, createdWorkDirs, userCreated,
				os.RemoveAll,
				func(u string) error {
					out, err := exec.Command("userdel", "-r", u).CombinedOutput()
					if err != nil {
						return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
					}
					return nil
				})
			if len(residues) == 0 {
				fmt.Fprintln(os.Stderr, "已回滚：本次创建的内容已全部清理，未残留半成品用户。")
			} else {
				fmt.Fprintln(os.Stderr, "警告: 回滚未能完全清理，以下内容需手动处理：")
				for _, r := range residues {
					fmt.Fprintf(os.Stderr, "  - %s\n", r)
				}
			}
			os.Exit(1)
		}

		// 1. 创建用户（-m 自动创建家目录，-s 指定 shell）
		fmt.Print("正在创建用户... ")
		if out, err := exec.Command("useradd", "-m", "-s", "/bin/bash", "-c", fullName, username).CombinedOutput(); err != nil {
			// useradd 失败意味着尚未创建任何内容，无需回滚
			fmt.Fprintf(os.Stderr, "\n错误: useradd 失败: %v\n%s\n", err, out)
			os.Exit(1)
		}
		userCreated = true
		fmt.Println("完成")

		// 2. 在每块数据盘上创建工作目录并建立符号链接
		for _, c := range candidates {
			workDir := filepath.Join(c.MountPoint, username)
			linkPath := filepath.Join(homeDir, filepath.Base(c.MountPoint))

			// 二次确认工作目录仍不存在：只把本次真正 mkdir 出来的目录纳入回滚清单，
			// 绝不删已存在的同名目录（预检与此刻之间若有人抢先创建，也在此拦下）
			if _, err := os.Lstat(workDir); err == nil {
				abort("错误: 工作目录 %s 已存在，中止创建（不会删除已存在的目录）", workDir)
			}

			fmt.Printf("正在创建工作目录 %s ... ", workDir)
			if err := os.MkdirAll(workDir, 0700); err != nil {
				abort("错误: 创建目录 %s 失败: %v", workDir, err)
			}
			createdWorkDirs = append(createdWorkDirs, workDir)
			if out, err := exec.Command("chown", username+":"+username, workDir).CombinedOutput(); err != nil {
				abort("错误: chown 失败: %v\n%s", err, out)
			}
			fmt.Println("完成")

			fmt.Printf("正在创建符号链接 %s ... ", linkPath)
			if err := os.Symlink(workDir, linkPath); err != nil {
				abort("错误: 创建符号链接失败: %v", err)
			}
			if out, err := exec.Command("chown", "-h", username+":"+username, linkPath).CombinedOutput(); err != nil {
				abort("错误: 链接 chown 失败: %v\n%s", err, out)
			}
			fmt.Println("完成")
		}

		// 3. 设置初始密码（chpasswd 以 root 运行，不经 pwquality，允许弱密码）
		fmt.Print("正在设置密码... ")
		chpasswd := exec.Command("chpasswd")
		chpasswd.Stdin = strings.NewReader(username + ":" + password + "\n")
		if out, err := chpasswd.CombinedOutput(); err != nil {
			abort("错误: 设置密码失败: %v\n%s", err, out)
		}
		fmt.Println("完成")

		writeAudit("user.add", username,
			fmt.Sprintf("家目录 %s + %d 个数据盘工作目录", homeDir, len(createdWorkDirs)))

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
		homeDir := filepath.Join("/home", username)

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

		// purge 模式下在删除前量一下释放多少空间（du 必须在 RemoveAll/userdel -r 之前跑），供审计
		var freed int64
		if userDelPurge {
			freed += dirSizeBytes(homeDir)
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
			if userDelPurge {
				freed += dirSizeBytes(dir)
			}
			fmt.Printf("正在删除 %s ... ", dir)
			if err := os.RemoveAll(dir); err != nil {
				fmt.Fprintf(os.Stderr, "\n错误: 删除 %s 失败: %v\n", dir, err)
				os.Exit(1)
			}
			fmt.Println("完成")
		}

		if userDelPurge {
			writeAudit("user.del.purge", username,
				fmt.Sprintf("释放 %s（家目录 + %d 个数据盘目录）", formatBytes(freed), len(dataDirs)))
		} else {
			writeAudit("user.del", username, "仅删除账号，保留家目录与数据")
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
		writeAudit("user.passwd", args[0], "")
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
