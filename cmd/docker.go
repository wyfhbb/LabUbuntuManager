package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

const dockerMirrorURL = "https://mirrors.bfsu.edu.cn/docker-ce"

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
				fmt.Fprintf(tw, "%s\t%s\t无权限\t可执行: usermod -aG docker %s\n", u.name, u.uid, u.name)
			}
		}
		tw.Flush()
	},
}

// ── 注册 ──────────────────────────────────────────────────────────────────────

var dockerCmd = &cobra.Command{
	Use:   "docker",
	Short: "Docker 管理（检测、安装、权限查看）",
}

func init() {
	dockerCmd.AddCommand(dockerCheckCmd, dockerInstallCmd, dockerPermCmd)
	rootCmd.AddCommand(dockerCmd)
}
