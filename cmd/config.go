package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
)

const (
	serverMgrLibDir = "/usr/local/lib/server-mgr"
	configFilePath  = serverMgrLibDir + "/config.conf"
)

// serverMgrConfig 收敛原先散落在各处的硬编码阈值。
//
// 落盘格式为 shell 可 source 的 KEY=VALUE，daily-disk-monitor.sh 直接 source
// 同一个文件读取保留天数，避免 Go 端与脚本端各写一份。
type serverMgrConfig struct {
	DiskWarnPercent  float64 // 分区使用率警戒线（百分比），超过后 MOTD 顶部告警
	DiskUserWarnGB   float64 // 单用户总占用告警线（GB），超过后 disk warn 点名写入 MOTD
	DiskLogKeepDays  int     // /var/log/disk-usage 下日志保留天数
	DiskCronTime     string  // 每日磁盘统计时间，格式 "分 时"
	InactiveCronTime string  // 每日不活跃用户检查时间，格式 "分 时"
	InactiveDays     int     // 超过多少天未登录视为不活跃
}

func defaultConfig() serverMgrConfig {
	return serverMgrConfig{
		DiskWarnPercent:  defaultDiskUsageWarnPercent,
		DiskUserWarnGB:   defaultDiskUserWarnGB,
		DiskLogKeepDays:  30,
		DiskCronTime:     "0 1",
		InactiveCronTime: "0 2",
		InactiveDays:     defaultInactiveDays,
	}
}

// parseConfig 解析 KEY=VALUE 配置内容。
// 未出现的键、无法解析或超出合理范围的值一律回落到默认值，
// 保证配置文件被改坏时命令仍能工作（MOTD 渲染路径不能因为配置报错而中断登录）。
func parseConfig(r io.Reader) serverMgrConfig {
	cfg := defaultConfig()

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)

		switch key {
		case "DISK_WARN_PERCENT":
			if v, err := strconv.ParseFloat(value, 64); err == nil && v > 0 && v <= 100 {
				cfg.DiskWarnPercent = v
			}
		case "DISK_USER_WARN_GB":
			if v, err := strconv.ParseFloat(value, 64); err == nil && v > 0 {
				cfg.DiskUserWarnGB = v
			}
		case "DISK_LOG_KEEP_DAYS":
			if v, err := strconv.Atoi(value); err == nil && v > 0 {
				cfg.DiskLogKeepDays = v
			}
		case "DISK_CRON_TIME":
			if isValidCronTime(value) {
				cfg.DiskCronTime = value
			}
		case "INACTIVE_CRON_TIME":
			if isValidCronTime(value) {
				cfg.InactiveCronTime = value
			}
		case "INACTIVE_DAYS":
			if v, err := strconv.Atoi(value); err == nil && v > 0 {
				cfg.InactiveDays = v
			}
		}
	}
	return cfg
}

// isValidCronTime 校验 "分 时" 形式的时间配置。
func isValidCronTime(spec string) bool {
	fields := strings.Fields(spec)
	if len(fields) != 2 {
		return false
	}
	minute, err := strconv.Atoi(fields[0])
	if err != nil || minute < 0 || minute > 59 {
		return false
	}
	hour, err := strconv.Atoi(fields[1])
	if err != nil || hour < 0 || hour > 23 {
		return false
	}
	return true
}

// cronExpr 把 "分 时" 展开成 cron 的五段表达式，如 "0 1" → "0 1 * * *"。
func cronExpr(spec string) string {
	fields := strings.Fields(spec)
	if !isValidCronTime(spec) {
		return "0 1 * * *"
	}
	return fields[0] + " " + fields[1] + " * * *"
}

// cronTimeDisplay 把 "分 时" 格式化成给人看的 "HH:MM"。
func cronTimeDisplay(spec string) string {
	fields := strings.Fields(spec)
	if !isValidCronTime(spec) {
		return spec
	}
	minute, _ := strconv.Atoi(fields[0])
	hour, _ := strconv.Atoi(fields[1])
	return fmt.Sprintf("%02d:%02d", hour, minute)
}

// renderConfigFile 生成配置文件内容（带注释，供管理员直接编辑）。
func renderConfigFile(cfg serverMgrConfig) string {
	var sb strings.Builder
	sb.WriteString("# server-mgr 配置文件\n")
	sb.WriteString("# 由 server-mgr install 生成，可直接编辑；格式为 shell 可 source 的 KEY=VALUE。\n")
	sb.WriteString("# 改动后需重新执行对应的 enable 命令才会更新 /etc/cron.d 下的定时任务。\n")
	sb.WriteString("\n")
	sb.WriteString("# 分区使用率警戒线（百分比）。超过后 disk / MOTD 输出标红，\n")
	sb.WriteString("# 并在 MOTD 顶部醒目提示该分区\n")
	sb.WriteString(fmt.Sprintf("DISK_WARN_PERCENT=%g\n", cfg.DiskWarnPercent))
	sb.WriteString("\n")
	sb.WriteString("# 单用户总占用告警线（GB）。每日统计跑完后，占用超过此值的用户\n")
	sb.WriteString("# 会被 disk warn 点名写入 MOTD 警告\n")
	sb.WriteString(fmt.Sprintf("DISK_USER_WARN_GB=%g\n", cfg.DiskUserWarnGB))
	sb.WriteString("\n")
	sb.WriteString("# /var/log/disk-usage 下每日报表保留天数\n")
	sb.WriteString(fmt.Sprintf("DISK_LOG_KEEP_DAYS=%d\n", cfg.DiskLogKeepDays))
	sb.WriteString("\n")
	sb.WriteString("# 每日磁盘统计执行时间，格式 \"分 时\"\n")
	sb.WriteString(fmt.Sprintf("DISK_CRON_TIME=%q\n", cfg.DiskCronTime))
	sb.WriteString("\n")
	sb.WriteString("# 每日不活跃用户检查执行时间，格式 \"分 时\"\n")
	sb.WriteString(fmt.Sprintf("INACTIVE_CRON_TIME=%q\n", cfg.InactiveCronTime))
	sb.WriteString("\n")
	sb.WriteString("# 超过多少天未登录视为不活跃用户\n")
	sb.WriteString(fmt.Sprintf("INACTIVE_DAYS=%d\n", cfg.InactiveDays))
	return sb.String()
}

// readConfigFile 读取配置文件；文件不存在或读取失败时返回默认值。
//
// 尚未执行过 install 的机器上可能还留着旧的 inactive-days.conf，
// 这里做一次兼容读取，避免升级二进制后阈值突然回到默认的 180 天。
func readConfigFile(path string) serverMgrConfig {
	f, err := os.Open(path)
	if err != nil {
		cfg := defaultConfig()
		if days := readInactiveConf(); days > 0 {
			cfg.InactiveDays = days
		}
		return cfg
	}
	defer f.Close()
	return parseConfig(f)
}

var (
	loadedConfig     serverMgrConfig
	loadedConfigOnce sync.Once
)

// config 返回当前进程的配置（首次调用时读盘，之后复用）。
func config() serverMgrConfig {
	loadedConfigOnce.Do(func() {
		loadedConfig = readConfigFile(configFilePath)
	})
	return loadedConfig
}

// writeConfig 覆盖写入配置文件。
func writeConfig(cfg serverMgrConfig) error {
	if err := os.MkdirAll(serverMgrLibDir, 0755); err != nil {
		return fmt.Errorf("无法创建目录 %s: %w", serverMgrLibDir, err)
	}
	if err := os.WriteFile(configFilePath, []byte(renderConfigFile(cfg)), 0644); err != nil {
		return fmt.Errorf("无法写入配置文件 %s: %w", configFilePath, err)
	}
	return nil
}

// ensureConfigFile 在配置文件缺失时按默认值创建，已存在则原样保留（不覆盖管理员的修改）。
// 创建时把旧的 motd/inactive-days.conf 阈值迁移进来并删除旧文件。
func ensureConfigFile() (created bool, err error) {
	if _, statErr := os.Stat(configFilePath); statErr == nil {
		return false, nil
	}

	cfg := defaultConfig()
	migrated := 0
	if days := readInactiveConf(); days > 0 {
		cfg.InactiveDays = days
		migrated = days
	}

	if err := writeConfig(cfg); err != nil {
		return false, err
	}
	if migrated > 0 {
		os.Remove(inactiveConfFile)
		fmt.Printf("已迁移旧配置: %s（%d 天）→ %s\n", inactiveConfFile, migrated, configFilePath)
	}
	return true, nil
}
