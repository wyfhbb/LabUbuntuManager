package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// 审计日志落盘路径（root only，文件 0600 / 目录 0700）。抽成变量以便测试替换。
var (
	auditLogDir  = "/var/log/server-mgr"
	auditLogPath = "/var/log/server-mgr/audit.log"
)

// sanitizeAuditValue 清洗字段值：去掉会破坏分隔文本结构的字符（换行、竖线）。
func sanitizeAuditValue(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "/")
	return strings.TrimSpace(s)
}

// formatAuditLine 组装一条审计记录（分隔文本，字段间 " | "，字段内 key=value）。
// target 为空时用 "-" 占位，details 为空时整段省略。
func formatAuditLine(t time.Time, invoker, action, target, details string) string {
	if invoker == "" {
		invoker = "unknown"
	}
	if target == "" {
		target = "-"
	}
	line := fmt.Sprintf("%s | 执行者=%s | 动作=%s | 目标=%s",
		t.Format(time.RFC3339),
		sanitizeAuditValue(invoker), sanitizeAuditValue(action), sanitizeAuditValue(target))
	if details != "" {
		line += " | 详情=" + sanitizeAuditValue(details)
	}
	return line
}

// writeAudit 追加一条审计记录。只在写操作**成功完成后**调用。
// 目录/文件按需创建（0700 / 0600）。写失败不阻断主流程——操作已经成功，只打警告到 stderr。
func writeAudit(action, target, details string) {
	if err := os.MkdirAll(auditLogDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "警告: 无法创建审计日志目录 %s: %v\n", auditLogDir, err)
		return
	}
	f, err := os.OpenFile(auditLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "警告: 无法写入审计日志 %s: %v\n", auditLogPath, err)
		return
	}
	defer f.Close()
	f.Chmod(0600) // 若文件此前以其他权限存在，纠正回 0600
	fmt.Fprintln(f, formatAuditLine(time.Now(), currentInvokingUser(), action, target, details))
}

// formatBytes 把字节数格式化成人类可读（B/KB/MB/GB/TB）。
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGT"[exp])
}

// dirSizeBytes 返回目录占用字节数（du -sb）；失败返回 0，不阻断删除流程。
func dirSizeBytes(path string) int64 {
	out, err := exec.Command("du", "-sb", path).Output()
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0
	}
	var n int64
	fmt.Sscanf(fields[0], "%d", &n)
	return n
}

// ── audit 查看命令 ────────────────────────────────────────────────────────────

var (
	auditUserFilter  string
	auditSinceFilter string
)

// parseAuditLine 从一条审计记录里取出时间、执行者、目标（供过滤用）。解析失败返回 ok=false。
func parseAuditLine(line string) (t time.Time, invoker, target string, ok bool) {
	parts := strings.Split(line, " | ")
	if len(parts) < 3 {
		return time.Time{}, "", "", false
	}
	ts, err := time.Parse(time.RFC3339, strings.TrimSpace(parts[0]))
	if err != nil {
		return time.Time{}, "", "", false
	}
	for _, p := range parts[1:] {
		p = strings.TrimSpace(p)
		if v, found := strings.CutPrefix(p, "执行者="); found {
			invoker = v
		} else if v, found := strings.CutPrefix(p, "目标="); found {
			target = v
		}
	}
	return ts, invoker, target, true
}

// targetContainsUser 判断 target 字段（可能是逗号/空格分隔的多个用户）里是否含指定用户。
func targetContainsUser(target, user string) bool {
	for _, f := range strings.FieldsFunc(target, func(r rune) bool { return r == ',' || r == ' ' }) {
		if f == user {
			return true
		}
	}
	return false
}

// auditLineMatches 判断一行是否满足过滤条件。
// user 为空则不按用户过滤（否则同时匹配执行者与目标）；since 为零值则不按时间过滤。
func auditLineMatches(line, user string, since time.Time) bool {
	t, invoker, target, ok := parseAuditLine(line)
	if !ok {
		return false
	}
	if !since.IsZero() && t.Before(since) {
		return false
	}
	if user != "" && invoker != user && !targetContainsUser(target, user) {
		return false
	}
	return true
}

// parseAuditSince 解析 --since 的时间（本地时区），支持纯日期或日期+时间。
func parseAuditSince(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析 %q", s)
}

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "查看写操作审计日志（需要 root）",
	Long: `查看 /var/log/server-mgr/audit.log 中记录的写操作（用户增删改、purge、换源、
镜像加速、MOTD、安装卸载等）。日志权限 0600，仅 root 可读；只记录成功完成的写操作。

支持按用户（同时匹配执行者与目标）和起始时间过滤：

  sudo server-mgr audit
  sudo server-mgr audit --user zhangsan
  sudo server-mgr audit --since 2026-07-01
  sudo server-mgr audit --user zhangsan --since "2026-07-01 12:00:00"`,
	Run: func(cmd *cobra.Command, args []string) {
		requireRoot()

		var since time.Time
		if auditSinceFilter != "" {
			t, err := parseAuditSince(auditSinceFilter)
			if err != nil {
				fmt.Fprintf(os.Stderr, "错误: --since 时间格式无效（用 2006-01-02 或 \"2006-01-02 15:04:05\"）: %v\n", err)
				os.Exit(1)
			}
			since = t
		}

		f, err := os.Open(auditLogPath)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Println("暂无审计记录（还没有执行过写操作，或日志尚未生成）")
				return
			}
			fmt.Fprintf(os.Stderr, "错误: 无法读取审计日志 %s: %v\n", auditLogPath, err)
			os.Exit(1)
		}
		defer f.Close()

		matched := 0
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 审计行可能较长（含命令行）
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			if !auditLineMatches(line, auditUserFilter, since) {
				continue
			}
			fmt.Println(line)
			matched++
		}
		if matched == 0 {
			fmt.Println("没有符合条件的审计记录")
		}
	},
}

func init() {
	auditCmd.Flags().StringVar(&auditUserFilter, "user", "", "只看涉及该用户（执行者或目标）的记录")
	auditCmd.Flags().StringVar(&auditSinceFilter, "since", "", "只看该时间之后的记录（如 2026-07-01 或 \"2026-07-01 12:00:00\"）")
	rootCmd.AddCommand(auditCmd)
}
