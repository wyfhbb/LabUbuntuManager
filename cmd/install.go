package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// 以下三个值由 makefile 通过 -ldflags -X 注入，直接 go build 时保持占位值。
var (
	buildVersion = "dev"
	buildCommit  = "unknown"
	buildTime    = "unknown"
)

// cronFileGlob 匹配本工具写入 /etc/cron.d 的全部定时任务，uninstall 据此清理。
const cronFileGlob = "/etc/cron.d/server-mgr-*"

// ── 安装辅助 ──────────────────────────────────────────────────────────────────

// ensureInstalled 把当前运行的二进制安装到 installedBinPath。
//
// disk monitor enable / motd set / user inactive monitor enable 共用此入口：
// 之前三处各自 copyFile，系统里 /usr/local/bin/server-mgr 到底是哪次编译
// 取决于最后执行的是哪个命令。
func ensureInstalled() error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法获取当前二进制路径: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(execPath); err == nil {
		execPath = resolved
	}

	switch installed, err := installBinary(execPath, installedBinPath); {
	case err != nil:
		return err
	case installed:
		fmt.Printf("二进制已安装: %s\n", installedBinPath)
	default:
		fmt.Printf("二进制已是当前运行的版本: %s\n", installedBinPath)
	}
	return nil
}

// installBinary 复制二进制到目标路径，返回是否实际复制过。
//
// 先写同目录临时文件再 rename：直接 O_TRUNC 覆盖正在运行的可执行文件会得到
// ETXTBSY，rename 则是原子替换，不影响已在运行的进程。
func installBinary(src, dst string) (bool, error) {
	if sameFile(src, dst) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return false, fmt.Errorf("无法创建目录 %s: %w", filepath.Dir(dst), err)
	}

	tmp := dst + ".new"
	if err := copyFile(src, tmp, 0755); err != nil {
		os.Remove(tmp)
		return false, fmt.Errorf("无法安装二进制到 %s: %w", dst, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return false, fmt.Errorf("无法替换 %s: %w", dst, err)
	}
	return true, nil
}

func sameFile(a, b string) bool {
	infoA, err := os.Stat(a)
	if err != nil {
		return false
	}
	infoB, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(infoA, infoB)
}

// installDiskUsageWrapper 写出 /usr/local/bin/disk-usage，供所有用户免 sudo 查看统计。
func installDiskUsageWrapper() error {
	if err := os.WriteFile(diskUsageWrapper, []byte(wrapperScript), 0755); err != nil {
		return fmt.Errorf("无法写入快捷命令 %s: %w", diskUsageWrapper, err)
	}
	return nil
}

func requireRoot() {
	if os.Getuid() != 0 {
		fmt.Fprintln(os.Stderr, "错误: 此命令需要 root 权限，请使用 sudo 执行")
		os.Exit(1)
	}
}

// ── install ──────────────────────────────────────────────────────────────────

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "安装 server-mgr 到系统路径（需要 root）",
	Long: `把当前二进制安装到 /usr/local/bin/server-mgr，安装 disk-usage 快捷命令，
并在 /usr/local/lib/server-mgr/config.conf 缺失时写入默认配置。

这是唯一的安装入口；disk monitor enable / motd set / user inactive monitor enable
也会复用同一套安装逻辑，不再各自复制二进制。`,
	Run: func(cmd *cobra.Command, args []string) {
		requireRoot()

		if err := os.MkdirAll(serverMgrLibDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 无法创建目录 %s: %v\n", serverMgrLibDir, err)
			os.Exit(1)
		}

		if err := ensureInstalled(); err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}

		if err := installDiskUsageWrapper(); err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("快捷命令已安装: %s\n", diskUsageWrapper)

		created, err := ensureConfigFile()
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}
		if created {
			fmt.Printf("默认配置已写入: %s\n", configFilePath)
		} else {
			fmt.Printf("配置已存在，保持不变: %s\n", configFilePath)
		}

		fmt.Println()
		fmt.Printf("已安装版本: %s (commit %s, 编译于 %s)\n", buildVersion, buildCommit, buildTime)
		fmt.Println()
		fmt.Println("后续可按需启用各功能：")
		fmt.Println("  sudo server-mgr disk monitor enable            # 每日磁盘统计")
		fmt.Println("  sudo server-mgr motd set                      # 自定义登录欢迎信息")
		fmt.Println("  sudo server-mgr user inactive monitor enable   # 不活跃用户检查")
	},
}

// ── version ──────────────────────────────────────────────────────────────────

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "显示版本、编译时间与 git commit",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("server-mgr %s\n", buildVersion)
		fmt.Printf("commit:    %s\n", buildCommit)
		fmt.Printf("编译时间:  %s\n", buildTime)

		// 提示当前运行的是否就是系统里安装的那份，便于确认部署是否生效
		if execPath, err := os.Executable(); err == nil {
			if resolved, err := filepath.EvalSymlinks(execPath); err == nil {
				execPath = resolved
			}
			fmt.Printf("当前路径:  %s\n", execPath)
			if _, err := os.Stat(installedBinPath); err == nil && !sameFile(execPath, installedBinPath) {
				fmt.Printf("提示: %s 是另一份二进制，可执行 sudo %s install 覆盖\n", installedBinPath, execPath)
			}
		}
	},
}

// ── uninstall ────────────────────────────────────────────────────────────────

var uninstallYes bool

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "卸载 server-mgr 及其所有系统改动（需要 root）",
	Long: `清理二进制、disk-usage 快捷命令、所有 /etc/cron.d/server-mgr-* 定时任务、
自定义 MOTD 脚本与 shell 注入，并恢复被禁用的系统默认 MOTD 脚本。

/var/log/disk-usage 下的历史统计数据与 /usr/local/lib/server-mgr 下的配置会保留。`,
	Run: func(cmd *cobra.Command, args []string) {
		requireRoot()

		crons, _ := filepath.Glob(cronFileGlob)

		fmt.Println("即将删除：")
		fmt.Printf("  二进制:      %s\n", installedBinPath)
		fmt.Printf("  快捷命令:    %s\n", diskUsageWrapper)
		fmt.Printf("  MOTD 脚本:   %s\n", motdScriptPath)
		if len(crons) > 0 {
			fmt.Printf("  定时任务:    %s\n", strings.Join(crons, ", "))
		} else {
			fmt.Println("  定时任务:    无")
		}
		fmt.Println("  shell 注入:  /etc/bash.bashrc、/etc/zsh/zshrc 中的 VSCode MOTD 片段")
		fmt.Println("保留：")
		fmt.Printf("  统计数据:    %s\n", diskLogDir)
		fmt.Printf("  配置与数据:  %s\n", serverMgrLibDir)

		if !uninstallYes {
			fmt.Print("\n确认卸载? (y/N): ")
			var confirm string
			fmt.Scanln(&confirm)
			if strings.TrimSpace(strings.ToLower(confirm)) != "y" {
				fmt.Println("已取消")
				return
			}
		}
		fmt.Println()

		// 1. 定时任务
		for _, path := range crons {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "警告: 无法删除 %s: %v\n", path, err)
				continue
			}
			fmt.Printf("已删除定时任务: %s\n", path)
		}

		// 2. 自定义 MOTD 脚本与 shell 注入，并恢复默认 MOTD
		if err := os.Remove(motdScriptPath); err == nil {
			fmt.Printf("已删除 MOTD 脚本: %s\n", motdScriptPath)
		} else if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "警告: 无法删除 %s: %v\n", motdScriptPath, err)
		}
		removeVscodeMotd(bashRcPath)
		removeVscodeMotd(zshRcPath)
		if restored := enableDefaultMotdScripts(); restored > 0 {
			fmt.Printf("已恢复 %d 个系统默认 MOTD 脚本\n", restored)
		}

		// 3. 快捷命令与二进制（正在运行的二进制可以被删除，不影响本次执行）
		if err := os.Remove(diskUsageWrapper); err == nil {
			fmt.Printf("已删除快捷命令: %s\n", diskUsageWrapper)
		} else if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "警告: 无法删除 %s: %v\n", diskUsageWrapper, err)
		}
		if err := os.Remove(installedBinPath); err == nil {
			fmt.Printf("已删除二进制: %s\n", installedBinPath)
		} else if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "警告: 无法删除 %s: %v\n", installedBinPath, err)
		}

		fmt.Println()
		fmt.Println("卸载完成。以下内容按设计保留，如需清理请手动删除：")
		fmt.Printf("  历史统计数据: %s\n", diskLogDir)
		fmt.Printf("  配置与欢迎语: %s\n", serverMgrLibDir)
	},
}

func init() {
	uninstallCmd.Flags().BoolVarP(&uninstallYes, "yes", "y", false, "跳过确认直接卸载")

	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(uninstallCmd)
}
