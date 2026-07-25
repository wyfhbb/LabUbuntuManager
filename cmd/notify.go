package cmd

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// 落盘路径：notify.conf 含密钥（0600），notify/state 是去重状态（0600）。抽成变量以便测试替换。
var (
	notifyConfigPath = serverMgrLibDir + "/notify.conf"
	notifyStatePath  = serverMgrLibDir + "/notify/state"
)

const defaultNotifySilenceHours = 24

// ── 配置 ──────────────────────────────────────────────────────────────────────

// notifyConfig 是告警推送配置（含密钥，落盘 0600）。
type notifyConfig struct {
	WeChatWebhook string
	SMTPHost      string
	SMTPPort      int
	SMTPUser      string
	SMTPPassword  string
	SMTPFrom      string
	SMTPTo        []string
	SMTPTLS       string // "starttls"（587，默认）/ "ssl"（465 隐式 TLS）/ "none"
	SilenceHours  int
}

func (c notifyConfig) hasAnyChannel() bool { return c.WeChatWebhook != "" || c.SMTPHost != "" }

func (c notifyConfig) silenceWindow() time.Duration {
	h := c.SilenceHours
	if h <= 0 {
		h = defaultNotifySilenceHours
	}
	return time.Duration(h) * time.Hour
}

// splitRecipients 把逗号 / 空格分隔的收件人拆成列表。
func splitRecipients(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// parseNotifyConfig 解析 notify.conf（KEY=VALUE），缺省值用默认。纯函数，便于测试。
func parseNotifyConfig(r io.Reader) notifyConfig {
	cfg := notifyConfig{SMTPPort: 587, SMTPTLS: "starttls", SilenceHours: defaultNotifySilenceHours}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "NOTIFY_WECHAT_WEBHOOK":
			cfg.WeChatWebhook = v
		case "NOTIFY_SMTP_HOST":
			cfg.SMTPHost = v
		case "NOTIFY_SMTP_PORT":
			if n, err := strconv.Atoi(v); err == nil {
				cfg.SMTPPort = n
			}
		case "NOTIFY_SMTP_USER":
			cfg.SMTPUser = v
		case "NOTIFY_SMTP_PASSWORD":
			cfg.SMTPPassword = v
		case "NOTIFY_SMTP_FROM":
			cfg.SMTPFrom = v
		case "NOTIFY_SMTP_TO":
			cfg.SMTPTo = splitRecipients(v)
		case "NOTIFY_SMTP_TLS":
			if v != "" {
				cfg.SMTPTLS = v
			}
		case "NOTIFY_SILENCE_HOURS":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.SilenceHours = n
			}
		}
	}
	return cfg
}

// formatNotifyConfig 把配置序列化为 notify.conf 文本。纯函数，与 parse 往返一致。
func formatNotifyConfig(cfg notifyConfig) string {
	var sb strings.Builder
	sb.WriteString("# server-mgr 告警推送配置（含密钥，权限 0600，勿提交到版本库）\n")
	sb.WriteString("# 由 server-mgr notify config 生成\n")
	fmt.Fprintf(&sb, "NOTIFY_WECHAT_WEBHOOK=%s\n", cfg.WeChatWebhook)
	fmt.Fprintf(&sb, "NOTIFY_SMTP_HOST=%s\n", cfg.SMTPHost)
	fmt.Fprintf(&sb, "NOTIFY_SMTP_PORT=%d\n", cfg.SMTPPort)
	fmt.Fprintf(&sb, "NOTIFY_SMTP_USER=%s\n", cfg.SMTPUser)
	fmt.Fprintf(&sb, "NOTIFY_SMTP_PASSWORD=%s\n", cfg.SMTPPassword)
	fmt.Fprintf(&sb, "NOTIFY_SMTP_FROM=%s\n", cfg.SMTPFrom)
	fmt.Fprintf(&sb, "NOTIFY_SMTP_TO=%s\n", strings.Join(cfg.SMTPTo, ","))
	fmt.Fprintf(&sb, "NOTIFY_SMTP_TLS=%s\n", cfg.SMTPTLS)
	fmt.Fprintf(&sb, "NOTIFY_SILENCE_HOURS=%d\n", cfg.SilenceHours)
	return sb.String()
}

func loadNotifyConfig() notifyConfig {
	f, err := os.Open(notifyConfigPath)
	if err != nil {
		return notifyConfig{SMTPPort: 587, SMTPTLS: "starttls", SilenceHours: defaultNotifySilenceHours}
	}
	defer f.Close()
	return parseNotifyConfig(f)
}

func saveNotifyConfig(cfg notifyConfig) error {
	if err := os.MkdirAll(filepath.Dir(notifyConfigPath), 0755); err != nil {
		return fmt.Errorf("无法创建目录 %s: %w", filepath.Dir(notifyConfigPath), err)
	}
	if err := os.WriteFile(notifyConfigPath, []byte(formatNotifyConfig(cfg)), 0600); err != nil {
		return fmt.Errorf("无法写入 %s: %w", notifyConfigPath, err)
	}
	return nil
}

// ── 去重状态 ──────────────────────────────────────────────────────────────────

// parseNotifyState 解析去重状态（每行 `key<TAB>unixts`）。纯函数。
func parseNotifyState(r io.Reader) map[string]time.Time {
	state := map[string]time.Time{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		if ts, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64); err == nil {
			state[parts[0]] = time.Unix(ts, 0)
		}
	}
	return state
}

// formatNotifyState 序列化去重状态（按 key 排序，输出稳定）。纯函数。
func formatNotifyState(state map[string]time.Time) string {
	keys := make([]string, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&sb, "%s\t%d\n", k, state[k].Unix())
	}
	return sb.String()
}

func loadNotifyState() map[string]time.Time {
	f, err := os.Open(notifyStatePath)
	if err != nil {
		return map[string]time.Time{}
	}
	defer f.Close()
	return parseNotifyState(f)
}

func saveNotifyState(state map[string]time.Time) error {
	if err := os.MkdirAll(filepath.Dir(notifyStatePath), 0700); err != nil {
		return err
	}
	return os.WriteFile(notifyStatePath, []byte(formatNotifyState(state)), 0600)
}

// ── 告警构建与去重 ────────────────────────────────────────────────────────────

// notifyAlert 是一条待推送告警。key 用于去重（同 key 在静默窗口内不重复推送）。
type notifyAlert struct {
	key   string
	title string
	body  string
}

// buildAlerts 由三个告警源构造告警列表。纯函数：输入判定结果，输出告警。
// 磁盘分区按挂载点各一条；GPU 异常一条（无卡不算异常，调用方需先排除）；需要重启一条。
func buildAlerts(overMounts []DiskUsage, fault *GPUFault, rebootRequired bool) []notifyAlert {
	var alerts []notifyAlert
	for _, m := range overMounts {
		alerts = append(alerts, notifyAlert{
			key:   "disk.partition:" + m.MountPoint,
			title: fmt.Sprintf("磁盘分区使用率告警：%s", m.MountPoint),
			body: fmt.Sprintf("挂载点 %s 使用率 %.0f%%（已用 %.0f/%.0f GB，设备 %s），已超过警戒线。",
				m.MountPoint, m.UsedPercent, m.UsedGB, m.TotalGB, m.Device),
		})
	}
	if fault != nil && !fault.isHardwareAbsent() {
		body := fault.Title
		if fault.Cause != "" {
			body += "\n成因: " + fault.Cause
		}
		if len(fault.Fixes) > 0 {
			body += "\n建议: " + fault.Fixes[0]
		}
		alerts = append(alerts, notifyAlert{key: "gpu.fault", title: "GPU 异常告警", body: body})
	}
	if rebootRequired {
		alerts = append(alerts, notifyAlert{
			key:   "reboot.required",
			title: "服务器需要重启",
			body:  "/var/run/reboot-required 存在，内核或核心库已更新，建议择机重启使其生效。",
		})
	}
	return alerts
}

// filterSilenced 滤掉静默窗口内已推送过的告警。纯函数。
func filterSilenced(alerts []notifyAlert, state map[string]time.Time, now time.Time, window time.Duration) []notifyAlert {
	var out []notifyAlert
	for _, a := range alerts {
		if last, ok := state[a.key]; ok && now.Sub(last) < window {
			continue
		}
		out = append(out, a)
	}
	return out
}

// ── 推送渠道 ──────────────────────────────────────────────────────────────────

// wechatPayload 构造企业微信机器人 text 消息体。纯函数。
func wechatPayload(content string) []byte {
	b, _ := json.Marshal(map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": content},
	})
	return b
}

func sendWeChat(webhook, content string) error {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(webhook, "application/json", bytes.NewReader(wechatPayload(content)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	// 企业微信成功返回 {"errcode":0,"errmsg":"ok"}
	var r struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if json.Unmarshal(body, &r) == nil && r.ErrCode != 0 {
		return fmt.Errorf("errcode=%d errmsg=%s", r.ErrCode, r.ErrMsg)
	}
	return nil
}

// buildEmailMessage 组装一封 UTF-8 纯文本邮件（主题做 MIME 编码，正文用 CRLF 换行）。纯函数。
func buildEmailMessage(from string, to []string, subject, body string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.BEncoding.Encode("UTF-8", subject))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	return []byte(b.String())
}

func sendSMTP(cfg notifyConfig, subject, body string) error {
	if cfg.SMTPHost == "" {
		return fmt.Errorf("未配置 SMTP 服务器")
	}
	if len(cfg.SMTPTo) == 0 {
		return fmt.Errorf("未配置收件人")
	}
	from := cfg.SMTPFrom
	if from == "" {
		from = cfg.SMTPUser
	}
	addr := net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(cfg.SMTPPort))
	msg := buildEmailMessage(from, cfg.SMTPTo, subject, body)

	var auth smtp.Auth
	if cfg.SMTPUser != "" {
		auth = smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPassword, cfg.SMTPHost)
	}

	if cfg.SMTPTLS == "ssl" {
		return sendSMTPImplicitTLS(addr, cfg.SMTPHost, auth, from, cfg.SMTPTo, msg)
	}
	// starttls / none：smtp.SendMail 在服务器支持时自动升级到 STARTTLS
	return smtp.SendMail(addr, auth, from, cfg.SMTPTo, msg)
}

// sendSMTPImplicitTLS 处理 465 端口的隐式 TLS（smtp.SendMail 不支持）。
func sendSMTPImplicitTLS(addr, host string, auth smtp.Auth, from string, to []string, msg []byte) error {
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Close()
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// sendAlert 把一条告警发到所有已配置渠道。至少一个渠道成功即返回 nil（并把失败渠道打到 stderr）；
// 所有渠道都失败才返回 error（调用方据此决定不记入去重状态，下次继续重试）。
func sendAlert(cfg notifyConfig, a notifyAlert) error {
	hostname, _ := os.Hostname()
	text := fmt.Sprintf("【server-mgr 告警】%s\n%s\n主机: %s\n时间: %s",
		a.title, a.body, hostname, time.Now().Format("2006-01-02 15:04:05"))

	var errs []string
	sent := false
	if cfg.WeChatWebhook != "" {
		if err := sendWeChat(cfg.WeChatWebhook, text); err != nil {
			errs = append(errs, "企业微信: "+err.Error())
		} else {
			sent = true
		}
	}
	if cfg.SMTPHost != "" {
		if err := sendSMTP(cfg, a.title, text); err != nil {
			errs = append(errs, "邮件: "+err.Error())
		} else {
			sent = true
		}
	}
	if !sent {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "警告: 告警 %q 部分渠道推送失败: %s\n", a.title, strings.Join(errs, "; "))
	}
	return nil
}

// ── 命令 ──────────────────────────────────────────────────────────────────────

func promptKeep(reader *bufio.Reader, label, current string) string {
	shown := current
	if shown == "" {
		shown = "空"
	}
	fmt.Printf("%s [%s]（回车保留，输入 - 清空）: ", label, shown)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	switch line {
	case "":
		return current
	case "-":
		return ""
	default:
		return line
	}
}

func promptKeepInt(reader *bufio.Reader, label string, current int) int {
	fmt.Printf("%s [%d]（回车保留）: ", label, current)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return current
	}
	if n, err := strconv.Atoi(line); err == nil {
		return n
	}
	return current
}

var notifyConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "配置告警推送渠道（企业微信 Webhook / SMTP 邮件，需要 root）",
	Long: `交互式配置告警推送，写入 /usr/local/lib/server-mgr/notify.conf（权限 0600，含密钥）。
回车保留当前值，输入 - 清空某项。SMTP 服务器留空则不启用邮件渠道。`,
	Run: func(cmd *cobra.Command, args []string) {
		requireRoot()
		cfg := loadNotifyConfig()
		reader := bufio.NewReader(os.Stdin)

		fmt.Println("── 告警推送配置 ──")
		cfg.WeChatWebhook = promptKeep(reader, "企业微信机器人 Webhook", cfg.WeChatWebhook)

		fmt.Println()
		cfg.SMTPHost = promptKeep(reader, "SMTP 服务器（留空=不启用邮件）", cfg.SMTPHost)
		if cfg.SMTPHost != "" {
			cfg.SMTPPort = promptKeepInt(reader, "SMTP 端口", cfg.SMTPPort)
			cfg.SMTPUser = promptKeep(reader, "SMTP 账号", cfg.SMTPUser)
			pwState := "空"
			if cfg.SMTPPassword != "" {
				pwState = "已设置"
			}
			pw := readHidden(reader, fmt.Sprintf("SMTP 密码 [%s]（回车保留，输入 - 清空）: ", pwState))
			switch pw {
			case "":
				// 保留
			case "-":
				cfg.SMTPPassword = ""
			default:
				cfg.SMTPPassword = pw
			}
			cfg.SMTPFrom = promptKeep(reader, "发件人地址", cfg.SMTPFrom)
			cfg.SMTPTo = splitRecipients(promptKeep(reader, "收件人（逗号分隔）", strings.Join(cfg.SMTPTo, ",")))
			cfg.SMTPTLS = promptKeep(reader, "加密方式 starttls/ssl/none", cfg.SMTPTLS)
		}

		fmt.Println()
		cfg.SilenceHours = promptKeepInt(reader, "静默窗口（小时，同一告警窗口内不重复推送）", cfg.SilenceHours)

		if err := saveNotifyConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\n已保存: %s（权限 0600）\n", notifyConfigPath)
		fmt.Println("可用 sudo server-mgr notify test 发一条测试消息验证。")
	},
}

var notifyTestCmd = &cobra.Command{
	Use:   "test",
	Short: "发一条测试消息验证推送配置（需要 root）",
	Run: func(cmd *cobra.Command, args []string) {
		requireRoot()
		cfg := loadNotifyConfig()
		if !cfg.hasAnyChannel() {
			fmt.Fprintln(os.Stderr, "错误: 未配置任何推送渠道，先运行 sudo server-mgr notify config")
			os.Exit(1)
		}

		hostname, _ := os.Hostname()
		text := fmt.Sprintf("【server-mgr 测试】这是一条测试告警，用于验证推送配置。\n主机: %s\n时间: %s",
			hostname, time.Now().Format("2006-01-02 15:04:05"))

		failed := false
		if cfg.WeChatWebhook != "" {
			fmt.Print("企业微信... ")
			if err := sendWeChat(cfg.WeChatWebhook, text); err != nil {
				fmt.Printf("失败: %v\n", err)
				failed = true
			} else {
				fmt.Println("已发送")
			}
		}
		if cfg.SMTPHost != "" {
			fmt.Print("邮件... ")
			if err := sendSMTP(cfg, "server-mgr 测试告警", text); err != nil {
				fmt.Printf("失败: %v\n", err)
				failed = true
			} else {
				fmt.Println("已发送")
			}
		}
		if failed {
			os.Exit(1)
		}
	},
}

var notifyCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "扫描告警源并推送（去重后，需要 root；每日磁盘统计后自动触发）",
	Long: `扫描分区使用率、GPU 状态、是否需要重启三个告警源，把新出现（或已过静默窗口）的
告警推送到已配置渠道。通常由每日磁盘统计脚本自动触发，也可手动执行。`,
	Run: func(cmd *cobra.Command, args []string) {
		requireRoot()
		runNotifyCheck()
	},
}

// runNotifyCheck 是 notify check 的主体：采集三源 → 构造告警 → 去重 → 推送 → 更新状态。
func runNotifyCheck() {
	cfg := loadNotifyConfig()
	if !cfg.hasAnyChannel() {
		fmt.Println("未配置任何推送渠道，跳过（先运行 sudo server-mgr notify config）")
		return
	}

	// 源 1：分区使用率超警戒线
	var overMounts []DiskUsage
	if provider := NewProcMountDiskUsageProvider(); provider != nil {
		if usages, err := provider.ListDiskUsage(); err == nil {
			overMounts = selectOverThresholdMounts(usages, config().DiskWarnPercent)
		}
	}

	// 源 2：GPU 异常（无卡不算异常）
	var fault *GPUFault
	if _, err := NewNvidiaSMIProvider().ListGPUs(); err != nil {
		if f := asGPUFault(err); !f.isHardwareAbsent() {
			fault = f
		}
	}

	// 源 3：需要重启
	rebootRequired := false
	if _, err := os.Stat("/var/run/reboot-required"); err == nil {
		rebootRequired = true
	}

	alerts := buildAlerts(overMounts, fault, rebootRequired)
	state := loadNotifyState()
	now := time.Now()
	window := cfg.silenceWindow()

	sent := 0
	for _, a := range filterSilenced(alerts, state, now, window) {
		if err := sendAlert(cfg, a); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 告警 %q 推送失败，未记入去重（下次重试）: %v\n", a.title, err)
			continue
		}
		state[a.key] = now // 成功推送才记入去重
		sent++
	}

	// 清理过期状态：超过静默窗口的条目已无去重意义，避免文件无限增长
	for k, t := range state {
		if now.Sub(t) > window {
			delete(state, k)
		}
	}
	if err := saveNotifyState(state); err != nil {
		fmt.Fprintf(os.Stderr, "警告: 无法保存去重状态 %s: %v\n", notifyStatePath, err)
	}
	fmt.Printf("检查完成：本次推送 %d 条告警\n", sent)
}

var notifyCmd = &cobra.Command{
	Use:   "notify",
	Short: "主动告警推送（企业微信 / 邮件）",
}

func init() {
	notifyCmd.AddCommand(notifyConfigCmd, notifyTestCmd, notifyCheckCmd)
	rootCmd.AddCommand(notifyCmd)
}
