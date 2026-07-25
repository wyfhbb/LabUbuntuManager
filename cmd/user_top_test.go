package cmd

import (
	"testing"
)

func TestAggregateUserProcesses(t *testing.T) {
	// 字段：user pid %cpu rss(KB) etimes comm
	// rss 单位 KB：10485760KB=10GiB, 1048576KB=1GiB, 9437184KB=9GiB
	out := "" +
		"alice 100 50.0 10485760 100000 python\n" + // 10GiB + 运行>1天 → 标记
		"alice 101 10.0 1048576 100000 java\n" + //     1GiB，不够大 → 不标记
		"bob 200 5.0 9437184 1000 chrome\n" + //         9GiB 但运行<1天 → 不标记
		"root 1 0.0 5000 500000 systemd\n" + //          很小
		"short\n" // 字段不足，跳过

	users, flagged := aggregateUserProcesses(out)

	if len(users) != 3 {
		t.Fatalf("期望 3 个用户，实际 %d：%+v", len(users), users)
	}
	// 按内存降序：alice(11GiB) > bob(9GiB) > root(极小)
	if users[0].user != "alice" || users[1].user != "bob" || users[2].user != "root" {
		t.Fatalf("用户排序错误：%+v", users)
	}

	alice := users[0]
	if alice.procs != 2 {
		t.Errorf("alice 进程数应为 2，实际 %d", alice.procs)
	}
	if alice.cpuPct < 59.9 || alice.cpuPct > 60.1 {
		t.Errorf("alice CPU 合计应约 60.0，实际 %.1f", alice.cpuPct)
	}
	wantRSS := int64(11) * 1024 * 1024 * 1024 // 10GiB + 1GiB
	if alice.rssBytes != wantRSS {
		t.Errorf("alice 内存合计应为 %d，实际 %d", wantRSS, alice.rssBytes)
	}

	// 只有 alice 的 10GiB 长跑进程被标记
	if len(flagged) != 1 {
		t.Fatalf("期望 1 个被标记进程，实际 %d：%+v", len(flagged), flagged)
	}
	if flagged[0].user != "alice" || flagged[0].pid != "100" {
		t.Errorf("被标记进程应为 alice/pid100，实际 %+v", flagged[0])
	}
}
