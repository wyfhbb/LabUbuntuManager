package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// dockerMirrorURL 是安装 docker-ce 软件包用的 apt 源，与拉镜像用的加速地址无关。
const dockerMirrorURL = "https://mirrors.bfsu.edu.cn/docker-ce"

const (
	dockerGroupName        = "docker"
	dockerDaemonConfigPath = "/etc/docker/daemon.json"
	dockerDaemonBackupPath = "/etc/docker/daemon.json.bak"
)

// defaultDockerMirrorsRaw 是内置的镜像加速地址，多个用逗号分隔。
// makefile 会通过 -ldflags -X 从 .env 的 DOCKER_MIRRORS 覆盖它，改地址不必动代码。
//
// 现状：国内公开镜像仓库大多已不可用，这里只内置一个确认可用的地址，
// 后续确认新地址后追加到 .env 的 DOCKER_MIRRORS 即可。
var defaultDockerMirrorsRaw = "https://docker.1ms.run"

// parseMirrorList 解析逗号或空白分隔的镜像地址列表，去重并校验协议前缀。
func parseMirrorList(raw string) ([]string, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})

	seen := make(map[string]struct{}, len(fields))
	var mirrors []string
	for _, f := range fields {
		m := strings.TrimRight(strings.TrimSpace(f), "/")
		if m == "" {
			continue
		}
		if !strings.HasPrefix(m, "https://") && !strings.HasPrefix(m, "http://") {
			return nil, fmt.Errorf("镜像地址必须以 http:// 或 https:// 开头: %s", f)
		}
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		mirrors = append(mirrors, m)
	}
	if len(mirrors) == 0 {
		return nil, fmt.Errorf("未提供有效的镜像地址")
	}
	return mirrors, nil
}

// mergeRegistryMirrors 把 registry-mirrors 写进已有的 daemon.json 内容，保留其余配置项。
// 空内容视为空对象。注意：JSON 对象的键顺序在重新编码后按字母序排列，值不受影响。
func mergeRegistryMirrors(existing []byte, mirrors []string) ([]byte, error) {
	conf := map[string]any{}
	if trimmed := bytes.TrimSpace(existing); len(trimmed) > 0 {
		if err := json.Unmarshal(trimmed, &conf); err != nil {
			return nil, fmt.Errorf("解析现有 %s 失败（请先修复或备份该文件）: %w", dockerDaemonConfigPath, err)
		}
	}
	conf["registry-mirrors"] = mirrors

	out, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("生成配置失败: %w", err)
	}
	return append(out, '\n'), nil
}

// currentRegistryMirrors 从 daemon.json 内容中读出已配置的镜像地址，解析失败返回 nil。
func currentRegistryMirrors(existing []byte) []string {
	var conf struct {
		Mirrors []string `json:"registry-mirrors"`
	}
	if json.Unmarshal(bytes.TrimSpace(existing), &conf) != nil {
		return nil
	}
	return conf.Mirrors
}

// dockerInstalled 检测 docker 命令是否存在
func dockerInstalled() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

// dockerGroupMembers 解析 /etc/group，返回 docker 组成员集合
func dockerGroupMembers() (map[string]struct{}, error) {
	f, err := os.Open("/etc/group")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	members := make(map[string]struct{})
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) < 4 || parts[0] != "docker" {
			continue
		}
		for _, m := range strings.Split(parts[3], ",") {
			m = strings.TrimSpace(m)
			if m != "" {
				members[m] = struct{}{}
			}
		}
		break
	}
	return members, nil
}

// ── docker check ─────────────────────────────────────────────────────────────

var dockerCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "检测 Docker 安装状态",
	Run: func(cmd *cobra.Command, args []string) {
		if !dockerInstalled() {
			fmt.Println("Docker 未安装")
			fmt.Println()
			fmt.Println("安装方法（使用北京外国语大学镜像源）：")
			fmt.Println()
			fmt.Printf("  # 使用 curl：\n")
			fmt.Printf("  export DOWNLOAD_URL=\"%s\"\n", dockerMirrorURL)
			fmt.Println("  curl -fsSL https://raw.githubusercontent.com/docker/docker-install/master/install.sh | sh")
			fmt.Println()
			fmt.Printf("  # 使用 wget：\n")
			fmt.Printf("  export DOWNLOAD_URL=\"%s\"\n", dockerMirrorURL)
			fmt.Println("  wget -O- https://raw.githubusercontent.com/docker/docker-install/master/install.sh | sh")
			fmt.Println()
			fmt.Println("或直接运行：sudo server-mgr docker install")
			return
		}

		out, err := exec.Command("docker", "--version").Output()
		if err != nil {
			fmt.Println("Docker 已安装（无法获取版本信息）")
		} else {
			fmt.Printf("Docker 已安装：%s\n", strings.TrimSpace(string(out)))
		}
	},
}

// ── docker install ────────────────────────────────────────────────────────────

var dockerInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "安装 Docker（使用北京外国语大学镜像源）",
	Run: func(cmd *cobra.Command, args []string) {
		if os.Getuid() != 0 {
			fmt.Fprintln(os.Stderr, "错误: 请使用 sudo 运行")
			os.Exit(1)
		}

		if dockerInstalled() {
			out, _ := exec.Command("docker", "--version").Output()
			fmt.Printf("Docker 已安装（%s），无需重复安装\n", strings.TrimSpace(string(out)))
			return
		}

		fmt.Printf("正在安装 Docker（镜像源：%s）...\n\n", dockerMirrorURL)

		var script string
		if _, err := exec.LookPath("curl"); err == nil {
			script = fmt.Sprintf(
				`export DOWNLOAD_URL="%s" && curl -fsSL https://raw.githubusercontent.com/docker/docker-install/master/install.sh | sh`,
				dockerMirrorURL,
			)
		} else if _, err := exec.LookPath("wget"); err == nil {
			script = fmt.Sprintf(
				`export DOWNLOAD_URL="%s" && wget -O- https://raw.githubusercontent.com/docker/docker-install/master/install.sh | sh`,
				dockerMirrorURL,
			)
		} else {
			fmt.Fprintln(os.Stderr, "错误: 未找到 curl 或 wget，请先安装其中之一")
			os.Exit(1)
		}

		installCmd := exec.Command("sh", "-c", script)
		installCmd.Stdout = os.Stdout
		installCmd.Stderr = os.Stderr
		installCmd.Stdin = os.Stdin
		if err := installCmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "错误: Docker 安装失败: %v\n", err)
			os.Exit(1)
		}

		fmt.Println()
		fmt.Println("Docker 安装完成！")
		fmt.Println("提示: 运行 'sudo server-mgr docker perm' 查看用户权限状态")
	},
}

// ── docker perm ───────────────────────────────────────────────────────────────

var dockerPermCmd = &cobra.Command{
	Use:   "perm",
	Short: "查看各普通用户的 Docker 使用权限",
	// 有子命令后必须显式 NoArgs：否则 "docker perm ad zhangsan" 这类拼错的子命令
	// 会被当成参数忽略，静默列出权限表并以 0 退出，看起来像执行成功了
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if !dockerInstalled() {
			fmt.Fprintln(os.Stderr, "错误: Docker 未安装，请先运行 'sudo server-mgr docker install'")
			os.Exit(1)
		}

		// 解析普通用户列表（复用与 user list 相同的过滤规则）
		f, err := os.Open("/etc/passwd")
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: 读取 /etc/passwd 失败: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()

		type userEntry struct {
			name string
			uid  string
		}
		var users []userEntry

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
			username, uid, shell := parts[0], parts[2], parts[6]
			var uidNum int
			fmt.Sscanf(uid, "%d", &uidNum)
			if uidNum < 1000 {
				continue
			}
			if shell == "/usr/sbin/nologin" || shell == "/bin/false" {
				continue
			}
			users = append(users, userEntry{name: username, uid: uid})
		}

		if len(users) == 0 {
			fmt.Println("未找到普通用户")
			return
		}

		dockerMembers, err := dockerGroupMembers()
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: 读取 docker 组信息失败: %v\n", err)
			os.Exit(1)
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "用户名\tUID\tDocker 权限\t备注")
		for _, u := range users {
			if _, ok := dockerMembers[u.name]; ok {
				fmt.Fprintf(tw, "%s\t%s\t有权限\t已加入 docker 组\n", u.name, u.uid)
			} else {
				fmt.Fprintf(tw, "%s\t%s\t无权限\t可执行: sudo server-mgr docker perm add %s\n", u.name, u.uid, u.name)
			}
		}
		tw.Flush()
	},
}

// ── docker perm add / del ────────────────────────────────────────────────────

// requireDockerInstalled 在 docker 未安装时退出。
func requireDockerInstalled() {
	if !dockerInstalled() {
		fmt.Fprintln(os.Stderr, "错误: Docker 未安装，请先运行 'sudo server-mgr docker install'")
		os.Exit(1)
	}
}

// isDockerGroupMember 判断用户是否已在 docker 组内。
func isDockerGroupMember(username string) bool {
	members, err := dockerGroupMembers()
	if err != nil {
		return false
	}
	_, ok := members[username]
	return ok
}

var dockerPermAddCmd = &cobra.Command{
	Use:   "add <用户名>",
	Short: "把用户加入 docker 组，使其免 sudo 使用 docker（需要 root）",
	Long: `把用户加入 docker 组。

注意：docker 组成员可以挂载宿主机任意目录到容器里以 root 身份读写，
等价于把 root 权限交给该用户，只对可信用户开放。

示例：
  sudo server-mgr docker perm add zhangsan`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		requireRoot()
		requireDockerInstalled()

		username := args[0]
		if _, err := user.Lookup(username); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 用户 %s 不存在\n", username)
			os.Exit(1)
		}

		if isDockerGroupMember(username) {
			fmt.Printf("用户 %s 已在 docker 组中，无需重复添加\n", username)
			return
		}

		fmt.Printf("即将把用户 %s 加入 %s 组。\n", username, dockerGroupName)
		fmt.Println(colorYellow + "⚠ docker 组成员可挂载宿主机任意目录并以 root 身份读写，等同于授予 root 权限。" + colorReset)
		fmt.Print("确认授权? (y/N): ")
		var confirm string
		fmt.Scanln(&confirm)
		if strings.TrimSpace(strings.ToLower(confirm)) != "y" {
			fmt.Println("已取消")
			return
		}

		if out, err := exec.Command("usermod", "-aG", dockerGroupName, username).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "错误: usermod 失败: %v\n%s\n", err, out)
			os.Exit(1)
		}

		writeAudit("docker.perm.add", username, "加入 docker 组（等价 root 权限）")

		fmt.Printf("用户 %s 已加入 docker 组\n", username)
		fmt.Println("提示: 该用户需重新登录后才会生效（当前会话可执行 newgrp docker 临时生效）")
	},
}

var dockerPermDelCmd = &cobra.Command{
	Use:   "del <用户名>",
	Short: "把用户移出 docker 组（需要 root）",
	Long: `把用户移出 docker 组，收回免 sudo 使用 docker 的权限。

示例：
  sudo server-mgr docker perm del zhangsan`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		requireRoot()
		requireDockerInstalled()

		username := args[0]
		if !isDockerGroupMember(username) {
			fmt.Printf("用户 %s 不在 docker 组中，无需操作\n", username)
			return
		}

		if out, err := exec.Command("gpasswd", "-d", username, dockerGroupName).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "错误: gpasswd 失败: %v\n%s\n", err, out)
			os.Exit(1)
		}

		writeAudit("docker.perm.del", username, "移出 docker 组")

		fmt.Printf("用户 %s 已移出 docker 组\n", username)
		fmt.Println("提示: 该用户已登录的会话仍持有旧的组身份，需重新登录后才彻底失效")
	},
}

// ── docker mirror set ────────────────────────────────────────────────────────

var dockerMirrorCmd = &cobra.Command{
	Use:   "mirror",
	Short: "Docker 镜像加速地址管理",
}

var dockerMirrorSetCmd = &cobra.Command{
	Use:   "set [镜像地址...]",
	Short: "配置 /etc/docker/daemon.json 的镜像加速地址（需要 root）",
	Long: `写入 /etc/docker/daemon.json 的 registry-mirrors，daemon.json 中其他配置项保持不变。

不带参数时使用内置默认地址（编译期可通过 .env 的 DOCKER_MIRRORS 覆盖）。

示例：
  sudo server-mgr docker mirror set
  sudo server-mgr docker mirror set https://docker.1ms.run`,
	Run: func(cmd *cobra.Command, args []string) {
		requireRoot()

		raw := defaultDockerMirrorsRaw
		if len(args) > 0 {
			raw = strings.Join(args, ",")
		}
		mirrors, err := parseMirrorList(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}

		if err := os.MkdirAll(filepath.Dir(dockerDaemonConfigPath), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 无法创建目录 %s: %v\n", filepath.Dir(dockerDaemonConfigPath), err)
			os.Exit(1)
		}

		existing, err := os.ReadFile(dockerDaemonConfigPath)
		if err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "错误: 无法读取 %s: %v\n", dockerDaemonConfigPath, err)
			os.Exit(1)
		}

		if old := currentRegistryMirrors(existing); len(old) > 0 {
			fmt.Printf("当前镜像地址: %s\n", strings.Join(old, ", "))
		}

		updated, err := mergeRegistryMirrors(existing, mirrors)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}

		// 已有配置先备份，改坏了可以还原
		if len(existing) > 0 {
			if err := os.WriteFile(dockerDaemonBackupPath, existing, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "错误: 无法备份 %s: %v\n", dockerDaemonConfigPath, err)
				os.Exit(1)
			}
			fmt.Printf("原配置已备份: %s\n", dockerDaemonBackupPath)
		}

		if err := os.WriteFile(dockerDaemonConfigPath, updated, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 无法写入 %s: %v\n", dockerDaemonConfigPath, err)
			os.Exit(1)
		}
		fmt.Printf("镜像地址已写入 %s：\n", dockerDaemonConfigPath)
		for _, m := range mirrors {
			fmt.Printf("  %s\n", m)
		}

		writeAudit("docker.mirror.set", "-", "registry-mirrors="+strings.Join(mirrors, ","))

		if !dockerInstalled() {
			fmt.Println("\n提示: 当前未检测到 docker，配置将在安装后生效")
			return
		}

		fmt.Println()
		fmt.Println("需要重启 Docker 守护进程后配置才生效。")
		fmt.Println(colorYellow + "⚠ 重启会中断正在运行的容器（带重启策略的容器会自动拉起）。" + colorReset)
		fmt.Print("现在重启 Docker? (y/N): ")
		var confirm string
		fmt.Scanln(&confirm)
		if strings.TrimSpace(strings.ToLower(confirm)) != "y" {
			fmt.Println("已跳过。稍后可手动执行: sudo systemctl restart docker")
			return
		}

		restart := exec.Command("systemctl", "restart", "docker")
		restart.Stdout = os.Stdout
		restart.Stderr = os.Stderr
		if err := restart.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 重启 Docker 失败: %v\n", err)
			fmt.Fprintln(os.Stderr, "可手动执行: sudo systemctl restart docker")
			os.Exit(1)
		}
		fmt.Println("Docker 已重启。可执行 docker info 确认镜像地址已生效")
	},
}

// ── 注册 ──────────────────────────────────────────────────────────────────────

var dockerCmd = &cobra.Command{
	Use:   "docker",
	Short: "Docker 管理（检测、安装、权限、镜像加速）",
}

func init() {
	dockerPermCmd.AddCommand(dockerPermAddCmd, dockerPermDelCmd)
	dockerMirrorCmd.AddCommand(dockerMirrorSetCmd)

	dockerCmd.AddCommand(dockerCheckCmd, dockerInstallCmd, dockerPermCmd, dockerMirrorCmd)
	rootCmd.AddCommand(dockerCmd)
}
