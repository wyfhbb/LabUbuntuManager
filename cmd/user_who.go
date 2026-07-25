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

// parseWhoLoginTime 从 who 输出的时间字段起解析登录时刻，返回它占用了几个字段。
//
// who 的时间格式随 locale 变（coreutils 按 hard_locale(LC_TIME) 二选一）：
//
//	C / POSIX ：`Jul 25 18:46`      —— 三个字段，且不带年份
//	其他 locale：`2026-07-25 18:46` —— 两个字段
//
// 两种都要认：cron、systemd 等环境里跑的就是 C locale，
// 只认后者会把来源列显示成时间、时长显示成未知。
func parseWhoLoginTime(fields []string, now time.Time) (time.Time, int, bool) {
	if len(fields) >= 2 {
		if t, err := time.ParseInLocation("2006-01-02 15:04", fields[0]+" "+fields[1], time.Local); err == nil {
			return t, 2, true
		}
	}
	if len(fields) >= 3 {
		if t, err := time.ParseInLocation("Jan 2 15:04", strings.Join(fields[:3], " "), time.Local); err == nil {
			// 该格式不带年份：按今年补，跨年时（12 月登录、1 月查看）回退一年
			t = time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
			if t.After(now.Add(24 * time.Hour)) {
				t = t.AddDate(-1, 0, 0)
			}
			return t, 3, true
		}
	}
	return time.Time{}, 0, false
}

// parseWhoSessions 解析 `who` 的输出为登录会话列表。
// who 每行形如：`alice pts/0 2026-07-25 10:00 (192.168.1.5)`，末段来源可选，
// 时间部分的格式随 locale 变，见 parseWhoLoginTime。
func parseWhoSessions(out string, now time.Time) []whoSession {
	var sessions []whoSession
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		user, tty := fields[0], fields[1]

		// 时间解析不出来时不再猜后面的字段：宁可来源留空，也不要把时间当成来源 IP
		duration := time.Duration(-1)
		var rest []string
		if login, used, ok := parseWhoLoginTime(fields[2:], now); ok {
			duration = now.Sub(login)
			rest = fields[2+used:]
		}

		host := ""
		if len(rest) > 0 {
			host = strings.TrimSuffix(strings.TrimPrefix(rest[0], "("), ")")
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
