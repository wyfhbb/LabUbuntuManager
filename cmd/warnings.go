package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MOTD 警告按来源分文件存放。
//
// 原先所有告警共用一个 warnings.txt 且整文件覆写，谁后写谁把别人抹掉 ——
// 磁盘告警和不活跃用户告警没法共存。改成一个来源一个文件，各写各的，
// 渲染时按文件名顺序拼接（数字前缀即显示顺序）。
const (
	warningSourceDisk     = "10-disk"
	warningSourceInactive = "20-inactive"
)

// 落盘路径以变量形式给出，便于测试替换为夹具目录（同 sysfsRoot 的做法）。
var (
	motdWarningsDir = motdDataDir + "/warnings.d"

	// legacyMotdWarningsFile 是拆成 warnings.d/ 之前的单文件，
	// 仅用于读取兼容与一次性迁移，不再写入。
	legacyMotdWarningsFile = motdDataDir + "/warnings.txt"
)

// warningSourceLabels 给告警来源配上人话说明，用于 motd status。
var warningSourceLabels = map[string]string{
	warningSourceDisk:     "磁盘占用超标",
	warningSourceInactive: "长期未登录用户",
}

// warningFilePath 返回某个告警来源的落盘路径。
func warningFilePath(source string) string {
	return filepath.Join(motdWarningsDir, source+".txt")
}

// writeMotdWarning 覆写某个告警来源的内容；content 为空表示清除该来源，
// 不影响其他来源已写入的告警。
func writeMotdWarning(source, content string) error {
	if err := migrateLegacyWarnings(); err != nil {
		return err
	}

	path := warningFilePath(source)
	if strings.TrimSpace(content) == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("无法清除告警文件 %s: %w", path, err)
		}
		return nil
	}

	if err := os.MkdirAll(motdWarningsDir, 0755); err != nil {
		return fmt.Errorf("无法创建目录 %s: %w", motdWarningsDir, err)
	}
	data := []byte(strings.TrimRight(content, "\n") + "\n")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("无法写入告警文件 %s: %w", path, err)
	}
	return nil
}

// readMotdWarnings 按文件名顺序读回所有来源的告警内容。
//
// 只读不迁移：MOTD 渲染可能以普通用户身份执行（VSCode 终端注入的那条），
// 没有写 /usr/local/lib 的权限。还没迁移过的机器上老文件照读，
// 避免升级二进制后已有告警凭空消失。
func readMotdWarnings() []string {
	var blocks []string

	if entries, err := os.ReadDir(motdWarningsDir); err == nil {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".txt") {
				names = append(names, entry.Name())
			}
		}
		sort.Strings(names)

		for _, name := range names {
			data, err := os.ReadFile(filepath.Join(motdWarningsDir, name))
			if err != nil {
				continue
			}
			if s := strings.TrimSpace(string(data)); s != "" {
				blocks = append(blocks, s)
			}
		}
	}

	if data, err := os.ReadFile(legacyMotdWarningsFile); err == nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			blocks = append(blocks, s)
		}
	}
	return blocks
}

// migrateLegacyWarnings 把老的单文件 warnings.txt 挪进 warnings.d/。
// 老文件的内容只可能来自 user inactive —— 当时它是唯一的写入方。
func migrateLegacyWarnings() error {
	data, err := os.ReadFile(legacyMotdWarningsFile)
	if err != nil {
		return nil // 不存在或读不了，没什么可迁的
	}

	if content := strings.TrimSpace(string(data)); content != "" {
		if err := os.MkdirAll(motdWarningsDir, 0755); err != nil {
			return fmt.Errorf("无法创建目录 %s: %w", motdWarningsDir, err)
		}
		// 新文件已存在说明迁移过了，不要用老内容覆盖更新的告警
		dst := warningFilePath(warningSourceInactive)
		if _, statErr := os.Stat(dst); os.IsNotExist(statErr) {
			if err := os.WriteFile(dst, []byte(content+"\n"), 0644); err != nil {
				return fmt.Errorf("无法迁移旧告警到 %s: %w", dst, err)
			}
		}
	}

	if err := os.Remove(legacyMotdWarningsFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("无法删除旧告警文件 %s: %w", legacyMotdWarningsFile, err)
	}
	return nil
}

// listMotdWarningSources 返回当前有内容的告警来源说明，供 motd status 展示。
func listMotdWarningSources() []string {
	entries, err := os.ReadDir(motdWarningsDir)
	if err != nil {
		return nil
	}

	var sources []string
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".txt")
		if entry.IsDir() || name == entry.Name() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(motdWarningsDir, entry.Name()))
		if err != nil || strings.TrimSpace(string(data)) == "" {
			continue
		}
		if label, ok := warningSourceLabels[name]; ok {
			sources = append(sources, fmt.Sprintf("%s（%s）", label, name))
		} else {
			sources = append(sources, name)
		}
	}
	sort.Strings(sources)
	return sources
}
