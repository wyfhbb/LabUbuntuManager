package cmd

import (
	"strings"
	"testing"
)

func TestDetectMirror(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"阿里云", "URIs: https://mirrors.aliyun.com/ubuntu\n", "阿里云"},
		{"清华大学", "URIs: https://mirrors.tuna.tsinghua.edu.cn/ubuntu\n", "清华大学"},
		{"中科大", "URIs: https://mirrors.ustc.edu.cn/ubuntu\n", "中科大"},
		{"北外", "URIs: https://mirrors.bfsu.edu.cn/ubuntu\n", "北京外国语大学"},
		{"官方", "URIs: http://archive.ubuntu.com/ubuntu\n", "Ubuntu 官方"},
		{"官方 ports", "URIs: http://ports.ubuntu.com/ubuntu-ports\n", "Ubuntu 官方"},
		{"镜像站 ports", "URIs: https://mirrors.ustc.edu.cn/ubuntu-ports\n", "中科大"},
		{"未知镜像", "URIs: https://mirror.example.com/ubuntu\n", "未知"},
		{"空内容", "", "未知"},
	}

	for _, c := range cases {
		if got := detectMirror(c.content); got != c.want {
			t.Errorf("%s: detectMirror = %q，期望 %q", c.name, got, c.want)
		}
	}
}

func TestUsesPorts(t *testing.T) {
	cases := map[string]bool{
		"amd64": false, "i386": false,
		"arm64": true, "armhf": true, "ppc64el": true, "riscv64": true, "s390x": true,
	}
	for arch, want := range cases {
		if got := usesPorts(arch); got != want {
			t.Errorf("usesPorts(%q) = %v，期望 %v", arch, got, want)
		}
	}
}

func TestRenderSources(t *testing.T) {
	cases := []struct {
		name        string
		mirror      string
		codename    string
		arch        string
		wantURIs    []string
		notWantURIs []string
	}{
		{
			name: "阿里云 amd64 走 ubuntu", mirror: "aliyun", codename: "noble", arch: "amd64",
			wantURIs:    []string{"URIs: https://mirrors.aliyun.com/ubuntu\n"},
			notWantURIs: []string{"ubuntu-ports"},
		},
		{
			name: "阿里云 arm64 走 ubuntu-ports", mirror: "aliyun", codename: "noble", arch: "arm64",
			wantURIs:    []string{"URIs: https://mirrors.aliyun.com/ubuntu-ports\n"},
			notWantURIs: []string{"URIs: https://mirrors.aliyun.com/ubuntu\n"},
		},
		{
			name: "官方 amd64 安全更新用独立域名", mirror: "official", codename: "noble", arch: "amd64",
			wantURIs: []string{"URIs: http://archive.ubuntu.com/ubuntu\n", "URIs: http://security.ubuntu.com/ubuntu\n"},
		},
		{
			name: "官方 arm64 全部走 ports", mirror: "official", codename: "noble", arch: "arm64",
			wantURIs:    []string{"URIs: http://ports.ubuntu.com/ubuntu-ports\n"},
			notWantURIs: []string{"archive.ubuntu.com", "security.ubuntu.com"},
		},
	}

	for _, c := range cases {
		got := renderSources(mirrors[c.mirror], c.codename, c.arch)
		for _, want := range c.wantURIs {
			if !strings.Contains(got, want) {
				t.Errorf("%s: 结果中缺少 %q\n%s", c.name, want, got)
			}
		}
		for _, notWant := range c.notWantURIs {
			if strings.Contains(got, notWant) {
				t.Errorf("%s: 结果中不应出现 %q\n%s", c.name, notWant, got)
			}
		}
	}
}

// codename 必须填进全部 4 个位置（主仓库 3 处 + security 1 处）。
func TestRenderSourcesCodename(t *testing.T) {
	got := renderSources(mirrors["tsinghua"], "resolute", "amd64")
	for _, want := range []string{
		"Suites: resolute resolute-updates resolute-backports\n",
		"Suites: resolute-security\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("缺少 %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "%!s") || strings.Contains(got, "%s") {
		t.Errorf("模板占位符未全部替换\n%s", got)
	}
}

func TestMirrorsSpecComplete(t *testing.T) {
	for name, m := range mirrors {
		if m.display == "" || m.archive == "" || m.security == "" || m.portsArchive == "" || m.portsSecurity == "" {
			t.Errorf("镜像源 %q 定义不完整: %+v", name, m)
		}
	}
}
