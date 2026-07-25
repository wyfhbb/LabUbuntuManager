package cmd

import (
	"strings"
	"testing"
	"time"
)

func TestFormatAuditLine(t *testing.T) {
	ts := time.Date(2026, 7, 25, 14, 3, 0, 0, time.UTC)

	line := formatAuditLine(ts, "alice", "user.del.purge", "bob", "释放 12.3GB")
	for _, want := range []string{"执行者=alice", "动作=user.del.purge", "目标=bob", "详情=释放 12.3GB"} {
		if !strings.Contains(line, want) {
			t.Errorf("行缺少 %q：%s", want, line)
		}
	}

	// target 为空 → "-"，details 为空 → 整段省略
	line = formatAuditLine(ts, "", "install", "", "")
	if !strings.Contains(line, "执行者=unknown") {
		t.Errorf("空执行者应记为 unknown：%s", line)
	}
	if !strings.Contains(line, "目标=-") {
		t.Errorf("空目标应记为 -：%s", line)
	}
	if strings.Contains(line, "详情=") {
		t.Errorf("空详情不应出现 详情= 段：%s", line)
	}
}

func TestSanitizeAuditValueStripsSeparators(t *testing.T) {
	// 竖线与换行会破坏分隔文本结构，必须被清洗掉
	line := formatAuditLine(time.Now(), "a|b", "act", "t\ngt", "de|tail")
	if strings.Count(line, "\n") != 0 {
		t.Errorf("字段里的换行未清洗：%q", line)
	}
	// 分隔符 " | " 只应来自结构本身（5 段：时间/执行者/动作/目标/详情 → 4 个分隔符）；
	// 字段内的竖线已被清洗，不会引入多余分隔符
	if got := strings.Count(line, " | "); got != 4 {
		t.Errorf("期望 4 个结构分隔符，实际 %d：%s", got, line)
	}
}

func TestParseAuditLineAndMatch(t *testing.T) {
	line := formatAuditLine(
		time.Date(2026, 7, 25, 14, 0, 0, 0, time.Local),
		"alice", "user.del.purge", "bob", "释放 1GB")

	tm, invoker, target, ok := parseAuditLine(line)
	if !ok {
		t.Fatalf("应能解析：%s", line)
	}
	if invoker != "alice" || target != "bob" {
		t.Fatalf("解析出的字段不对：invoker=%q target=%q", invoker, target)
	}
	if tm.Year() != 2026 || tm.Month() != 7 || tm.Day() != 25 {
		t.Fatalf("解析出的时间不对：%v", tm)
	}

	// 按用户过滤：执行者与目标都应命中
	if !auditLineMatches(line, "alice", time.Time{}) {
		t.Error("按执行者 alice 应命中")
	}
	if !auditLineMatches(line, "bob", time.Time{}) {
		t.Error("按目标 bob 应命中")
	}
	if auditLineMatches(line, "carol", time.Time{}) {
		t.Error("无关用户 carol 不应命中")
	}

	// 按时间过滤
	if auditLineMatches(line, "", time.Date(2026, 7, 26, 0, 0, 0, 0, time.Local)) {
		t.Error("since 晚于记录时间，不应命中")
	}
	if !auditLineMatches(line, "", time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)) {
		t.Error("since 早于记录时间，应命中")
	}
}

func TestParseAuditLineRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "not a log line", "缺少时间 | 执行者=x"} {
		if _, _, _, ok := parseAuditLine(bad); ok {
			t.Errorf("不应把 %q 当作合法记录", bad)
		}
	}
}

func TestTargetContainsUser(t *testing.T) {
	if !targetContainsUser("alice, bob, carol", "bob") {
		t.Error("逗号分隔的多用户目标应能命中其中之一")
	}
	if targetContainsUser("alice", "ali") {
		t.Error("不应做前缀匹配")
	}
}

func TestParseAuditSince(t *testing.T) {
	if _, err := parseAuditSince("2026-07-01"); err != nil {
		t.Errorf("纯日期应可解析：%v", err)
	}
	if _, err := parseAuditSince("2026-07-01 12:30:00"); err != nil {
		t.Errorf("日期+时间应可解析：%v", err)
	}
	if _, err := parseAuditSince("昨天"); err == nil {
		t.Error("非法时间应报错")
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{
		0:                             "0B",
		512:                           "512B",
		1024:                          "1.0KB",
		8 * 1024 * 1024 * 1024:        "8.0GB",
		int64(3) * 1024 * 1024 * 1024: "3.0GB",
	}
	for n, want := range cases {
		if got := formatBytes(n); got != want {
			t.Errorf("formatBytes(%d)=%q，期望 %q", n, got, want)
		}
	}
}
