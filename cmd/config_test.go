package cmd

import (
	"strings"
	"testing"
)

func TestParseConfigReadsAllKeys(t *testing.T) {
	content := `# server-mgr 配置文件
DISK_WARN_PERCENT=90
DISK_LOG_KEEP_DAYS=7
DISK_CRON_TIME="30 3"
INACTIVE_CRON_TIME='15 4'
INACTIVE_DAYS=365
UNKNOWN_KEY=whatever
没有等号的行
`

	cfg := parseConfig(strings.NewReader(content))

	if cfg.DiskWarnPercent != 90 {
		t.Errorf("DISK_WARN_PERCENT = %v，期望 90", cfg.DiskWarnPercent)
	}
	if cfg.DiskLogKeepDays != 7 {
		t.Errorf("DISK_LOG_KEEP_DAYS = %v，期望 7", cfg.DiskLogKeepDays)
	}
	if cfg.DiskCronTime != "30 3" {
		t.Errorf("DISK_CRON_TIME = %q，期望 \"30 3\"", cfg.DiskCronTime)
	}
	if cfg.InactiveCronTime != "15 4" {
		t.Errorf("INACTIVE_CRON_TIME = %q，期望 \"15 4\"", cfg.InactiveCronTime)
	}
	if cfg.InactiveDays != 365 {
		t.Errorf("INACTIVE_DAYS = %v，期望 365", cfg.InactiveDays)
	}
}

func TestParseConfigFallsBackToDefaultsOnBadValues(t *testing.T) {
	content := `DISK_WARN_PERCENT=abc
DISK_LOG_KEEP_DAYS=-1
DISK_CRON_TIME="70 3"
INACTIVE_CRON_TIME="0 99"
INACTIVE_DAYS=0
`

	cfg := parseConfig(strings.NewReader(content))
	want := defaultConfig()

	if cfg != want {
		t.Fatalf("非法值应全部回落到默认值\n得到: %+v\n期望: %+v", cfg, want)
	}
}

func TestParseConfigEmptyContentUsesDefaults(t *testing.T) {
	if cfg := parseConfig(strings.NewReader("")); cfg != defaultConfig() {
		t.Fatalf("空配置应使用默认值，得到 %+v", cfg)
	}
}

func TestIsValidCronTime(t *testing.T) {
	valid := []string{"0 1", "59 23", "30 3"}
	for _, spec := range valid {
		if !isValidCronTime(spec) {
			t.Errorf("isValidCronTime(%q) = false，期望 true", spec)
		}
	}

	invalid := []string{"", "1", "60 1", "0 24", "-1 5", "a b", "0 1 2"}
	for _, spec := range invalid {
		if isValidCronTime(spec) {
			t.Errorf("isValidCronTime(%q) = true，期望 false", spec)
		}
	}
}

func TestCronExprAndDisplay(t *testing.T) {
	if got := cronExpr("30 3"); got != "30 3 * * *" {
		t.Errorf("cronExpr(\"30 3\") = %q", got)
	}
	if got := cronExpr("乱写"); got != "0 1 * * *" {
		t.Errorf("非法输入应回落到默认时间，得到 %q", got)
	}
	if got := cronTimeDisplay("0 1"); got != "01:00" {
		t.Errorf("cronTimeDisplay(\"0 1\") = %q，期望 01:00", got)
	}
	if got := cronTimeDisplay("5 23"); got != "23:05" {
		t.Errorf("cronTimeDisplay(\"5 23\") = %q，期望 23:05", got)
	}
}

// renderConfigFile 的输出要能被 parseConfig 读回来，否则 install 写出的文件自己都读不了。
func TestRenderConfigFileRoundTrip(t *testing.T) {
	cfg := serverMgrConfig{
		DiskWarnPercent:  85.5,
		DiskUserWarnGB:   250,
		DiskLogKeepDays:  14,
		DiskCronTime:     "30 3",
		InactiveCronTime: "45 4",
		InactiveDays:     90,
	}

	got := parseConfig(strings.NewReader(renderConfigFile(cfg)))
	if got != cfg {
		t.Fatalf("回读结果与写入不一致\n得到: %+v\n期望: %+v", got, cfg)
	}
}
