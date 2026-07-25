package cmd

import (
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{-1, "?"},
		{5 * time.Minute, "5m"},
		{2*time.Hour + 13*time.Minute, "2h13m"},
		{27 * time.Hour, "1d3h"},
		{0, "0m"},
	}
	for _, c := range cases {
		if got := formatDuration(c.d); got != c.want {
			t.Errorf("formatDuration(%v)=%q，期望 %q", c.d, got, c.want)
		}
	}
}

func TestParseWhoSessions(t *testing.T) {
	// 用 7 月的日期避开 DST 边界，保证时长计算稳定
	now := time.Date(2026, 7, 25, 12, 35, 0, 0, time.Local)
	out := "" +
		"alice    pts/0        2026-07-25 10:00 (192.168.1.5)\n" +
		"root     tty1         2026-07-24 09:00\n" +
		"bob      pts/1        2026-07-25 12:30 (10.0.0.2)\n" +
		"garbage\n"

	sessions := parseWhoSessions(out, now)
	if len(sessions) != 3 {
		t.Fatalf("期望 3 个会话（garbage 行被跳过），实际 %d：%+v", len(sessions), sessions)
	}

	byUser := map[string]whoSession{}
	for _, s := range sessions {
		byUser[s.user] = s
	}

	if s := byUser["alice"]; s.kind != "SSH" || s.source != "192.168.1.5" || s.detail != "pts/0" {
		t.Errorf("alice 会话解析错误：%+v", s)
	}
	if got := formatDuration(byUser["alice"].duration); got != "2h35m" {
		t.Errorf("alice 时长应为 2h35m，实际 %s", got)
	}
	if s := byUser["root"]; s.kind != "本地" || s.source != "-" {
		t.Errorf("无来源的本地会话解析错误：%+v", s)
	}
	if got := formatDuration(byUser["root"].duration); got != "1d3h" {
		t.Errorf("root 时长应为 1d3h，实际 %s", got)
	}
}

func TestParseVscodeSessions(t *testing.T) {
	now := time.Now()
	out := "" +
		"alice 1000 7200 /home/alice/.vscode-server/bin/abc/node --foo\n" +
		"alice 1001 100 /home/alice/.vscode-server/bin/abc/node --bar\n" +
		"bob 2000 300 /usr/bin/python train.py\n" +
		"carol 3000 500 /home/carol/.vscode-server/cli/servers/xyz/node\n"

	sessions := parseVscodeSessions(out, now)
	if len(sessions) != 2 {
		t.Fatalf("期望 2 个 VSCode 会话（alice、carol），实际 %d：%+v", len(sessions), sessions)
	}

	byUser := map[string]whoSession{}
	for _, s := range sessions {
		byUser[s.user] = s
	}
	if _, ok := byUser["bob"]; ok {
		t.Error("非 vscode 进程的用户 bob 不应产生会话")
	}
	// alice 有两个 vscode 进程，应取运行最久的那个（7200s → pid 1000）
	if s := byUser["alice"]; s.detail != "pid 1000" {
		t.Errorf("alice 应取运行最久的进程 pid 1000，实际 %+v", s)
	}
	if got := formatDuration(byUser["alice"].duration); got != "2h0m" {
		t.Errorf("alice VSCode 时长应为 2h0m，实际 %s", got)
	}
	if byUser["carol"].kind != "VSCode Remote(推断)" {
		t.Errorf("carol 会话类型错误：%+v", byUser["carol"])
	}
}
