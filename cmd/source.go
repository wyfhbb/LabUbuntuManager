package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
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
	"ports.ubuntu.com":             "Ubuntu 官方",
}

// mirrorSpec 一个镜像源的基址。x86（amd64/i386）与其余架构（arm64 等）在
// 各镜像站是两套独立路径：前者在 ubuntu/，后者在 ubuntu-ports/。
type mirrorSpec struct {
	display       string
	archive       string // x86 主仓库
	security      string // x86 安全更新
	portsArchive  string // 非 x86 主仓库
	portsSecurity string // 非 x86 安全更新
}

var mirrors = map[string]mirrorSpec{
	"aliyun": {
		display:       "阿里云",
		archive:       "https://mirrors.aliyun.com/ubuntu",
		security:      "https://mirrors.aliyun.com/ubuntu",
		portsArchive:  "https://mirrors.aliyun.com/ubuntu-ports",
		portsSecurity: "https://mirrors.aliyun.com/ubuntu-ports",
	},
	"tsinghua": {
		display:       "清华大学",
		archive:       "https://mirrors.tuna.tsinghua.edu.cn/ubuntu",
		security:      "https://mirrors.tuna.tsinghua.edu.cn/ubuntu",
		portsArchive:  "https://mirrors.tuna.tsinghua.edu.cn/ubuntu-ports",
		portsSecurity: "https://mirrors.tuna.tsinghua.edu.cn/ubuntu-ports",
	},
	"ustc": {
		display:       "中科大",
		archive:       "https://mirrors.ustc.edu.cn/ubuntu",
		security:      "https://mirrors.ustc.edu.cn/ubuntu",
		portsArchive:  "https://mirrors.ustc.edu.cn/ubuntu-ports",
		portsSecurity: "https://mirrors.ustc.edu.cn/ubuntu-ports",
	},
	"bfsu": {
		display:       "北京外国语大学",
		archive:       "https://mirrors.bfsu.edu.cn/ubuntu",
		security:      "https://mirrors.bfsu.edu.cn/ubuntu",
		portsArchive:  "https://mirrors.bfsu.edu.cn/ubuntu-ports",
		portsSecurity: "https://mirrors.bfsu.edu.cn/ubuntu-ports",
	},
	"official": {
		display:       "Ubuntu 官方",
		archive:       "http://archive.ubuntu.com/ubuntu",
		security:      "http://security.ubuntu.com/ubuntu",
		portsArchive:  "http://ports.ubuntu.com/ubuntu-ports",
		portsSecurity: "http://ports.ubuntu.com/ubuntu-ports",
	},
}

// sourcesTemplate DEB822 源文件模板：主仓库 URI、3 个 codename、安全更新 URI、1 个 codename。
const sourcesTemplate = `Types: deb
URIs: %s
Suites: %s %s-updates %s-backports
Components: main restricted universe multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg

Types: deb
URIs: %s
Suites: %s-security
Components: main restricted universe multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg
`

// usesPorts 判断该 dpkg 架构的包是否位于 ubuntu-ports 而非 ubuntu。
func usesPorts(arch string) bool {
	return arch != "amd64" && arch != "i386"
}

// renderSources 按架构选择 URI，生成完整的源文件内容。
func renderSources(m mirrorSpec, codename, arch string) string {
	archive, security := m.archive, m.security
	if usesPorts(arch) {
		archive, security = m.portsArchive, m.portsSecurity
	}
	return fmt.Sprintf(sourcesTemplate, archive, codename, codename, codename, security, codename)
}

// detectDebArch 返回 dpkg 架构名，dpkg 不可用时回退到本二进制的编译架构。
func detectDebArch() string {
	if out, err := exec.Command("dpkg", "--print-architecture").Output(); err == nil {
		if arch := strings.TrimSpace(string(out)); arch != "" {
			return arch
		}
	}
	switch runtime.GOARCH {
	case "amd64":
		return "amd64"
	case "386":
		return "i386"
	case "arm64":
		return "arm64"
	case "arm":
		return "armhf"
	default:
		return runtime.GOARCH
	}
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

// readUbuntuSources 读取 DEB822 源文件，文件不存在时给出系统版本相关的提示。
func readUbuntuSources() (string, error) {
	data, err := os.ReadFile(ubuntuSourcesPath)
	if err == nil {
		return string(data), nil
	}
	if os.IsNotExist(err) {
		return "", fmt.Errorf("未找到 %s\n"+
			"本命令依赖 Ubuntu 24.04 起启用的 DEB822 源格式；更早的版本用的是 /etc/apt/sources.list 单行格式，暂不支持",
			ubuntuSourcesPath)
	}
	return "", fmt.Errorf("读取 %s 失败: %w", ubuntuSourcesPath, err)
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
		content, err := readUbuntuSources()
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}

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
		spec, ok := mirrors[mirror]
		if !ok {
			fmt.Fprintf(os.Stderr, "错误: 不支持的镜像源 %q，可选: aliyun / tsinghua / ustc / bfsu / official\n", mirror)
			os.Exit(1)
		}

		codename, err := readOSCodename()
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}
		arch := detectDebArch()

		oldContent, err := readUbuntuSources()
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}

		// 首次换源时备份出厂源；已有备份则保留，避免被上一次换源的结果覆盖
		if _, err := os.Stat(ubuntuSourcesBakPath); os.IsNotExist(err) {
			if err := os.WriteFile(ubuntuSourcesBakPath, []byte(oldContent), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "写入备份 %s 失败: %v\n", ubuntuSourcesBakPath, err)
				os.Exit(1)
			}
			fmt.Printf("已备份原始源至 %s\n", ubuntuSourcesBakPath)
		} else {
			fmt.Printf("备份 %s 已存在，保留不覆盖\n", ubuntuSourcesBakPath)
		}

		newContent := renderSources(spec, codename, arch)
		if err := os.WriteFile(ubuntuSourcesPath, []byte(newContent), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "写入 %s 失败: %v\n", ubuntuSourcesPath, err)
			os.Exit(1)
		}

		fmt.Printf("已切换至 %s（Ubuntu %s，架构 %s）\n\n", spec.display, codename, arch)

		fmt.Println("正在执行 apt-get update ...")
		if err := runAptUpdate(); err != nil {
			fmt.Fprintf(os.Stderr, "\napt-get update 失败: %v\n", err)
			// 新源不可用（镜像站尚未同步该版本、架构无对应仓库等），回滚到换源前的内容
			if rbErr := os.WriteFile(ubuntuSourcesPath, []byte(oldContent), 0644); rbErr != nil {
				fmt.Fprintf(os.Stderr, "自动回滚失败: %v\n请手动执行 server-mgr source restore\n", rbErr)
			} else {
				fmt.Fprintf(os.Stderr, "已自动回滚 %s 至换源前的内容\n", ubuntuSourcesPath)
			}
			writeAudit("source.set", spec.display, fmt.Sprintf("失败并回滚，codename=%s arch=%s", codename, arch))
			os.Exit(1)
		}

		writeAudit("source.set", spec.display, fmt.Sprintf("codename=%s arch=%s", codename, arch))
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

		writeAudit("source.restore", "-", "从 "+ubuntuSourcesBakPath+" 还原")

		fmt.Println("正在执行 apt-get update ...")
		if err := runAptUpdate(); err != nil {
			fmt.Fprintf(os.Stderr, "\napt-get update 失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("\n还原完成。")
	},
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
