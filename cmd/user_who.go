package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// whoSession 表示一个当前会话（登录会话或推断出的 VSCode Remote 会话）。
type whoSession struct {
	user     string
	kind     string        // "SSH" / "本地" / "VSCode Remote(推断)"
	source   string        // 来源 IP / 显示，未知用 "-"
	duration time.Duration // 会话时长，负值表示无法解析
	detail   string        // tty 或 "pid N"
}

// formatDuration 把时长格式化成 1d3h / 2h13m / 5m；负值（未知）显示 "?"。
func formatDuration(d time.Duration) string {
	if d < 0 {
		return "?"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

// parseWhoSessions 解析 `who` 的输出为登录会话列表。
// who 每行形如：`alice pts/0 2026-07-25 10:00 (192.168.1.5)`，末段来源可选。
func parseWhoSessions(out string, now time.Time) []whoSession {
	var sessions []whoSession
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		user, tty, date, tm := fields[0], fields[1], fields[2], fields[3]

		duration := time.Duration(-1)
		if login, err := time.ParseInLocation("2006-01-02 15:04", date+" "+tm, time.Local); err == nil {
			duration = now.Sub(login)
		}

		host := ""
		if len(fields) >= 5 {
			host = strings.TrimSuffix(strings.TrimPrefix(fields[4], "("), ")")
		}

		s := whoSession{user: user, duration: duration, detail: tty}
		if host != "" && host != ":0" {
			s.kind, s.source = "SSH", host
		} else {
			s.kind, s.source = "本地", "-"
			if host == ":0" {
				s.source = ":0"
			}
		}
		sessions = append(sessions, s)
	}
	return sessions
}

// parseVscodeSessions 从 `ps -eo user=,pid=,etimes=,args=` 的输出里尽力识别 VSCode Remote 会话。
// VSCode Remote 不是登录会话，只能靠 `.vscode-server` 进程推断：每个有该进程的用户算一个会话，
// 取运行最久的那个进程代表会话起始时间。
func parseVscodeSessions(out string, now time.Time) []whoSession {
	type agg struct {
		maxElapsed int
		pid        string
	}
	byUser := map[string]*agg{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		user, pid, etimesStr := fields[0], fields[1], fields[2]
		args := strings.Join(fields[3:], " ")
		if !strings.Contains(args, ".vscode-server") {
			continue
		}
		var etimes int
		fmt.Sscanf(etimesStr, "%d", &etimes)
		if a := byUser[user]; a == nil || etimes > a.maxElapsed {
			byUser[user] = &agg{maxElapsed: etimes, pid: pid}
		}
	}

	var sessions []whoSession
	for user, a := range byUser {
		sessions = append(sessions, whoSession{
			user:     user,
			kind:     "VSCode Remote(推断)",
			source:   "-",
			duration: time.Duration(a.maxElapsed) * time.Second,
			detail:   "pid " + a.pid,
		})
	}
	return sessions
}

var userWhoCmd = &cobra.Command{
	Use:   "who",
	Short: "查看当前登录会话（SSH 来源 IP、登录时长，含尽力识别的 VSCode Remote 会话）",
	Long: `列出当前的登录会话：SSH 来源 IP、登录时长、tty。
VSCode Remote 不是登录会话，通过扫描各用户的 .vscode-server 进程尽力识别，标为"推断"。

所有用户可用，无需 root。`,
	Run: func(cmd *cobra.Command, args []string) {
		now := time.Now()
		var sessions []whoSession

		if out, err := exec.Command("who").Output(); err == nil {
			sessions = append(sessions, parseWhoSessions(string(out), now)...)
		}
		if out, err := exec.Command("ps", "-eo", "user=,pid=,etimes=,args=").Output(); err == nil {
			sessions = append(sessions, parseVscodeSessions(string(out), now)...)
		}

		if len(sessions) == 0 {
			fmt.Println("当前没有登录会话")
			return
		}

		sort.Slice(sessions, func(i, j int) bool {
			if sessions[i].user != sessions[j].user {
				return sessions[i].user < sessions[j].user
			}
			return sessions[i].kind < sessions[j].kind
		})

		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "用户\t类型\t来源\t时长\t会话")
		for _, s := range sessions {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				s.user, s.kind, s.source, formatDuration(s.duration), s.detail)
		}
		tw.Flush()
	},
}

func init() {
	userCmd.AddCommand(userWhoCmd)
}
