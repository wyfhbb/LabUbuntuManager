package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

const (
	ubuntuSourcesPath    = "/etc/apt/sources.list.d/ubuntu.sources"
	ubuntuSourcesBakPath = "/etc/apt/sources.list.d/ubuntu.sources.bak"
	osReleasePath        = "/etc/os-release"
)

// 已知镜像站域名 → 显示名
var knownMirrors = map[string]string{
	"mirrors.aliyun.com":           "阿里云",
	"mirrors.tuna.tsinghua.edu.cn": "清华大学",
	"mirrors.ustc.edu.cn":          "中科大",
	"mirrors.bfsu.edu.cn":          "北京外国语大学",
	"archive.ubuntu.com":           "Ubuntu 官方",
	"security.ubuntu.com":          "Ubuntu 官方",
}

// mirrorTemplates DEB822 格式模板，4 个 %s 均为 codename。
var mirrorTemplates = map[string]string{
	"aliyun": `Types: deb
URIs: https://mirrors.aliyun.com/ubuntu
Suites: %s %s-updates %s-backports
Components: main restricted universe multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg

Types: deb
URIs: https://mirrors.aliyun.com/ubuntu
Suites: %s-security
Components: main restricted universe multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg
`,
	"tsinghua": `Types: deb
URIs: https://mirrors.tuna.tsinghua.edu.cn/ubuntu
Suites: %s %s-updates %s-backports
Components: main restricted universe multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg

Types: deb
URIs: https://mirrors.tuna.tsinghua.edu.cn/ubuntu
Suites: %s-security
Components: main restricted universe multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg
`,
	"ustc": `Types: deb
URIs: https://mirrors.ustc.edu.cn/ubuntu
Suites: %s %s-updates %s-backports
Components: main restricted universe multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg

Types: deb
URIs: https://mirrors.ustc.edu.cn/ubuntu
Suites: %s-security
Components: main restricted universe multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg
`,
	"bfsu": `Types: deb
URIs: https://mirrors.bfsu.edu.cn/ubuntu
Suites: %s %s-updates %s-backports
Components: main restricted universe multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg

Types: deb
URIs: https://mirrors.bfsu.edu.cn/ubuntu
Suites: %s-security
Components: main restricted universe multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg
`,
	"official": `Types: deb
URIs: http://archive.ubuntu.com/ubuntu
Suites: %s %s-updates %s-backports
Components: main restricted universe multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg

Types: deb
URIs: http://security.ubuntu.com/ubuntu
Suites: %s-security
Components: main restricted universe multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg
`,
}

// readOSCodename 从 /etc/os-release 读取 VERSION_CODENAME。
func readOSCodename() (string, error) {
	f, err := os.Open(osReleasePath)
	if err != nil {
		return "", fmt.Errorf("打开 %s 失败: %w", osReleasePath, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "VERSION_CODENAME=") {
			codename := strings.TrimPrefix(line, "VERSION_CODENAME=")
			codename = strings.Trim(codename, `"`)
			return codename, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("读取 %s 失败: %w", osReleasePath, err)
	}
	return "", fmt.Errorf("未在 %s 中找到 VERSION_CODENAME", osReleasePath)
}

// detectMirror 从源文件内容中识别当前镜像站名称。
func detectMirror(content string) string {
	for domain, name := range knownMirrors {
		if strings.Contains(content, domain) {
			return name
		}
	}
	return "未知"
}

// runAptUpdate 执行 apt-get update 并将输出流式打印到终端。
func runAptUpdate() error {
	cmd := exec.Command("apt-get", "update")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

var sourceShowCmd = &cobra.Command{
	Use:   "show",
	Short: "查看当前 APT 镜像源",
	Run: func(cmd *cobra.Command, args []string) {
		data, err := os.ReadFile(ubuntuSourcesPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取 %s 失败: %v\n", ubuntuSourcesPath, err)
			os.Exit(1)
		}
		content := string(data)

		fmt.Printf("源文件: %s\n", ubuntuSourcesPath)
		fmt.Printf("当前镜像源: %s\n", detectMirror(content))
		fmt.Printf("\n--- %s ---\n", ubuntuSourcesPath)
		fmt.Print(content)

		if _, err := os.Stat(ubuntuSourcesBakPath); err == nil {
			fmt.Printf("\n备份文件存在: %s\n", ubuntuSourcesBakPath)
		}
	},
}

var sourceSetCmd = &cobra.Command{
	Use:   "set <mirror>",
	Short: "切换 APT 镜像源 (aliyun / tsinghua / ustc / bfsu / official)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if os.Getuid() != 0 {
			fmt.Fprintln(os.Stderr, "错误: 此操作需要 root 权限，请使用 sudo 运行")
			os.Exit(1)
		}

		mirror := strings.ToLower(args[0])
		tmpl, ok := mirrorTemplates[mirror]
		if !ok {
			fmt.Fprintf(os.Stderr, "错误: 不支持的镜像源 %q，可选: aliyun / tsinghua / ustc / bfsu / official\n", mirror)
			os.Exit(1)
		}

		codename, err := readOSCodename()
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}

		// 备份
		data, err := os.ReadFile(ubuntuSourcesPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取 %s 失败: %v\n", ubuntuSourcesPath, err)
			os.Exit(1)
		}
		if err := os.WriteFile(ubuntuSourcesBakPath, data, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "写入备份 %s 失败: %v\n", ubuntuSourcesBakPath, err)
			os.Exit(1)
		}
		fmt.Printf("已备份原始源至 %s\n", ubuntuSourcesBakPath)

		// 写入新源（4 个 %s 均为 codename）
		newContent := fmt.Sprintf(tmpl, codename, codename, codename, codename)
		if err := os.WriteFile(ubuntuSourcesPath, []byte(newContent), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "写入 %s 失败: %v\n", ubuntuSourcesPath, err)
			os.Exit(1)
		}

		mirrorName := knownMirrors[mirrorDomain(mirror)]
		if mirrorName == "" {
			mirrorName = mirror
		}
		fmt.Printf("已切换至 %s（Ubuntu %s）\n\n", mirrorName, codename)

		fmt.Println("正在执行 apt-get update ...")
		if err := runAptUpdate(); err != nil {
			fmt.Fprintf(os.Stderr, "\napt-get update 失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("\n换源完成。")
	},
}

var sourceRestoreCmd = &cobra.Command{
	Use:   "restore",
	Short: "从备份还原 APT 镜像源",
	Run: func(cmd *cobra.Command, args []string) {
		if os.Getuid() != 0 {
			fmt.Fprintln(os.Stderr, "错误: 此操作需要 root 权限，请使用 sudo 运行")
			os.Exit(1)
		}

		data, err := os.ReadFile(ubuntuSourcesBakPath)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "错误: 备份文件 %s 不存在，无法还原\n", ubuntuSourcesBakPath)
			} else {
				fmt.Fprintf(os.Stderr, "读取备份失败: %v\n", err)
			}
			os.Exit(1)
		}

		if err := os.WriteFile(ubuntuSourcesPath, data, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "写入 %s 失败: %v\n", ubuntuSourcesPath, err)
			os.Exit(1)
		}

		fmt.Printf("已从 %s 还原\n\n", ubuntuSourcesBakPath)

		fmt.Println("正在执行 apt-get update ...")
		if err := runAptUpdate(); err != nil {
			fmt.Fprintf(os.Stderr, "\napt-get update 失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("\n还原完成。")
	},
}

// mirrorDomain 返回镜像名称对应的主域名。
func mirrorDomain(mirror string) string {
	switch mirror {
	case "aliyun":
		return "mirrors.aliyun.com"
	case "tsinghua":
		return "mirrors.tuna.tsinghua.edu.cn"
	case "ustc":
		return "mirrors.ustc.edu.cn"
	case "bfsu":
		return "mirrors.bfsu.edu.cn"
	case "official":
		return "archive.ubuntu.com"
	default:
		return ""
	}
}

var sourceCmd = &cobra.Command{
	Use:   "source",
	Short: "管理 APT 镜像源",
}

func init() {
	sourceCmd.AddCommand(sourceShowCmd)
	sourceCmd.AddCommand(sourceSetCmd)
	sourceCmd.AddCommand(sourceRestoreCmd)
	rootCmd.AddCommand(sourceCmd)
}
