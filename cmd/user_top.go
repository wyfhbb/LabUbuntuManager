package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// 标记"长期占用大内存进程"的阈值：常驻内存 ≥ 8 GiB 且运行 ≥ 1 天。
const (
	bigMemRSSBytes = int64(8) * 1024 * 1024 * 1024
	longRunSeconds = 24 * 3600
)

// userProcAgg 是某个用户所有进程的聚合。
type userProcAgg struct {
	user     string
	procs    int
	cpuPct   float64
	rssBytes int64
}

// flaggedProc 是被标记的长期大内存进程。
type flaggedProc struct {
	user     string
	pid      string
	rssBytes int64
	elapsed  time.Duration
	comm     string
}

// aggregateUserProcesses 解析 `ps -eo user=,pid=,%cpu=,rss=,etimes=,comm=` 的输出，
// 按用户聚合 CPU/内存，并挑出长期占用大内存的进程。两个结果均已排好序（按内存降序）。
func aggregateUserProcesses(psOut string) ([]userProcAgg, []flaggedProc) {
	byUser := map[string]*userProcAgg{}
	var flagged []flaggedProc

	for _, line := range strings.Split(psOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		user, pid := fields[0], fields[1]
		cpu, _ := strconv.ParseFloat(fields[2], 64)
		rssKB, _ := strconv.ParseInt(fields[3], 10, 64)
		etimes, _ := strconv.ParseInt(fields[4], 10, 64)
		comm := strings.Join(fields[5:], " ")
		rssBytes := rssKB * 1024

		a := byUser[user]
		if a == nil {
			a = &userProcAgg{user: user}
			byUser[user] = a
		}
		a.procs++
		a.cpuPct += cpu
		a.rssBytes += rssBytes

		if rssBytes >= bigMemRSSBytes && etimes >= longRunSeconds {
			flagged = append(flagged, flaggedProc{
				user: user, pid: pid, rssBytes: rssBytes,
				elapsed: time.Duration(etimes) * time.Second, comm: comm,
			})
		}
	}

	users := make([]userProcAgg, 0, len(byUser))
	for _, a := range byUser {
		users = append(users, *a)
	}
	sort.Slice(users, func(i, j int) bool {
		if users[i].rssBytes != users[j].rssBytes {
			return users[i].rssBytes > users[j].rssBytes
		}
		return users[i].user < users[j].user
	})
	sort.Slice(flagged, func(i, j int) bool {
		if flagged[i].rssBytes != flagged[j].rssBytes {
			return flagged[i].rssBytes > flagged[j].rssBytes
		}
		return flagged[i].elapsed > flagged[j].elapsed
	})
	return users, flagged
}

var userTopCmd = &cobra.Command{
	Use:   "top",
	Short: "按用户聚合的 CPU / 内存占用排行，并标出长期占用大内存的进程",
	Long: `把所有进程按属主聚合，给出每个用户的进程数、CPU 占用、常驻内存（RSS），
按内存降序排列；并单独列出长期占用大内存的进程（RSS ≥ 8GiB 且运行 ≥ 1 天）。

所有用户可用，无需 root。`,
	Run: func(cmd *cobra.Command, args []string) {
		out, err := exec.Command("ps", "-eo", "user=,pid=,%cpu=,rss=,etimes=,comm=").Output()
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: 执行 ps 失败: %v\n", err)
			os.Exit(1)
		}
		users, flagged := aggregateUserProcesses(string(out))

		fmt.Println("按用户聚合（按内存占用降序）：")
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "用户\t进程数\tCPU%\t内存(RSS)")
		for _, u := range users {
			fmt.Fprintf(tw, "%s\t%d\t%.1f\t%s\n", u.user, u.procs, u.cpuPct, formatBytes(u.rssBytes))
		}
		tw.Flush()

		fmt.Printf("\n长期占用大内存的进程（RSS ≥ %s 且运行 ≥ 1 天）：\n", formatBytes(bigMemRSSBytes))
		if len(flagged) == 0 {
			fmt.Println("  本次没有符合条件的进程")
			return
		}
		tw2 := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw2, "  用户\tPID\t内存\t运行时长\t命令")
		for _, p := range flagged {
			fmt.Fprintf(tw2, "  %s\t%s\t%s\t%s\t%s\n",
				p.user, p.pid, formatBytes(p.rssBytes), formatDuration(p.elapsed), p.comm)
		}
		tw2.Flush()
	},
}

func init() {
	userCmd.AddCommand(userTopCmd)
}
