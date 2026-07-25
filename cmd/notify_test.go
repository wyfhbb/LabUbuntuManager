package cmd

import (
	"strings"
	"testing"
	"time"
)

func TestNotifyConfigRoundTrip(t *testing.T) {
	cfg := notifyConfig{
		WeChatWebhook: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=abc",
		SMTPHost:      "smtp.example.com",
		SMTPPort:      465,
		SMTPUser:      "bot@example.com",
		SMTPPassword:  "s3cr3t",
		SMTPFrom:      "bot@example.com",
		SMTPTo:        []string{"admin@example.com", "ops@example.com"},
		SMTPTLS:       "ssl",
		SilenceHours:  12,
	}
	got := parseNotifyConfig(strings.NewReader(formatNotifyConfig(cfg)))

	if got.WeChatWebhook != cfg.WeChatWebhook || got.SMTPHost != cfg.SMTPHost ||
		got.SMTPPort != cfg.SMTPPort || got.SMTPUser != cfg.SMTPUser ||
		got.SMTPPassword != cfg.SMTPPassword || got.SMTPFrom != cfg.SMTPFrom ||
		got.SMTPTLS != cfg.SMTPTLS || got.SilenceHours != cfg.SilenceHours {
		t.Fatalf("往返不一致：\n原始 %+v\n解析 %+v", cfg, got)
	}
	if strings.Join(got.SMTPTo, ",") != "admin@example.com,ops@example.com" {
		t.Errorf("收件人往返错误：%v", got.SMTPTo)
	}
}

func TestParseNotifyConfigDefaults(t *testing.T) {
	cfg := parseNotifyConfig(strings.NewReader(""))
	if cfg.SMTPPort != 587 || cfg.SMTPTLS != "starttls" || cfg.SilenceHours != defaultNotifySilenceHours {
		t.Errorf("空配置应回落默认值，实际 %+v", cfg)
	}
	if cfg.hasAnyChannel() {
		t.Error("空配置不应有任何渠道")
	}
}

func TestSilenceWindow(t *testing.T) {
	if got := (notifyConfig{SilenceHours: 6}).silenceWindow(); got != 6*time.Hour {
		t.Errorf("窗口应为 6h，实际 %v", got)
	}
	// 非法/零值回落默认
	if got := (notifyConfig{SilenceHours: 0}).silenceWindow(); got != defaultNotifySilenceHours*time.Hour {
		t.Errorf("零值应回落默认 %dh，实际 %v", defaultNotifySilenceHours, got)
	}
}

func TestBuildAlerts(t *testing.T) {
	mounts := []DiskUsage{
		{Device: "/dev/nvme0n1", MountPoint: "/data", TotalGB: 1000, UsedGB: 950, UsedPercent: 95},
	}
	fault := &GPUFault{Kind: gpuDriverMismatch, Title: "驱动版本不一致", Cause: "升级后没重启", Fixes: []string{"sudo reboot"}}

	alerts := buildAlerts(mounts, fault, true)
	if len(alerts) != 3 {
		t.Fatalf("期望 3 条告警（磁盘+GPU+重启），实际 %d：%+v", len(alerts), alerts)
	}

	keys := map[string]bool{}
	for _, a := range alerts {
		keys[a.key] = true
	}
	for _, want := range []string{"disk.partition:/data", "gpu.fault", "reboot.required"} {
		if !keys[want] {
			t.Errorf("缺少告警 key %q", want)
		}
	}

	// 无卡故障不应产生 GPU 告警
	absent := &GPUFault{Kind: gpuNoNvidiaHardware, Title: "本机没有 NVIDIA 显卡"}
	if got := buildAlerts(nil, absent, false); len(got) != 0 {
		t.Errorf("无卡+无其它源应产生 0 条告警，实际 %d：%+v", len(got), got)
	}
}

func TestFilterSilenced(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	window := 24 * time.Hour
	alerts := []notifyAlert{
		{key: "disk.partition:/data", title: "A"},
		{key: "reboot.required", title: "B"},
		{key: "gpu.fault", title: "C"},
	}
	state := map[string]time.Time{
		"disk.partition:/data": now.Add(-1 * time.Hour),  // 窗口内 → 静默
		"gpu.fault":            now.Add(-25 * time.Hour), // 已过窗口 → 应重发
	}

	got := filterSilenced(alerts, state, now, window)
	gotKeys := map[string]bool{}
	for _, a := range got {
		gotKeys[a.key] = true
	}
	if gotKeys["disk.partition:/data"] {
		t.Error("窗口内的告警应被静默")
	}
	if !gotKeys["reboot.required"] {
		t.Error("从未推送过的告警应发送")
	}
	if !gotKeys["gpu.fault"] {
		t.Error("已过静默窗口的告警应重新发送")
	}
}

func TestNotifyStateRoundTrip(t *testing.T) {
	now := time.Unix(1770000000, 0)
	state := map[string]time.Time{
		"reboot.required":      now,
		"disk.partition:/data": now.Add(-3 * time.Hour),
	}
	got := parseNotifyState(strings.NewReader(formatNotifyState(state)))
	if len(got) != 2 {
		t.Fatalf("往返后条目数不对：%v", got)
	}
	if !got["reboot.required"].Equal(now) {
		t.Errorf("时间戳往返错误：%v", got["reboot.required"])
	}
}

func TestWechatPayload(t *testing.T) {
	b := string(wechatPayload("磁盘 95%"))
	if !strings.Contains(b, `"msgtype":"text"`) || !strings.Contains(b, `"content":"磁盘 95%"`) {
		t.Errorf("企业微信消息体格式错误：%s", b)
	}
}

func TestBuildEmailMessage(t *testing.T) {
	msg := string(buildEmailMessage("bot@x.com", []string{"a@x.com", "b@x.com"}, "测试主题", "第一行\n第二行"))
	if !strings.Contains(msg, "From: bot@x.com\r\n") {
		t.Error("缺少 From 头")
	}
	if !strings.Contains(msg, "To: a@x.com, b@x.com\r\n") {
		t.Error("收件人头拼接错误")
	}
	if !strings.Contains(msg, "=?UTF-8?") {
		t.Error("中文主题应做 MIME 编码")
	}
	if !strings.Contains(msg, "第一行\r\n第二行") {
		t.Error("正文换行应转成 CRLF")
	}
}
