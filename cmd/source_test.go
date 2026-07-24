package cmd

import "testing"

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
		{"未知镜像", "URIs: https://mirror.example.com/ubuntu\n", "未知"},
		{"空内容", "", "未知"},
	}

	for _, c := range cases {
		if got := detectMirror(c.content); got != c.want {
			t.Errorf("%s: detectMirror = %q，期望 %q", c.name, got, c.want)
		}
	}
}
