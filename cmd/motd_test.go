package cmd

import (
	"strings"
	"testing"
)

// 老版本按行前缀猜片段边界，会把紧邻其后的 if 块首行一并删掉，
// 留下孤立的语句体和多余的 fi —— 所有用户的登录 shell 都会报语法错误。
func TestStripVscodeMotdKeepsAdjacentIfBlock(t *testing.T) {
	content := "# /etc/bash.bashrc\n" +
		"\n" +
		vscodeMotdLegacyMarker + "\n" +
		"if [ -n \"$VSCODE_IPC_HOOK_CLI\" ] || [ \"$TERM_PROGRAM\" = \"vscode\" ]; then\n" +
		"\trun-parts /etc/update-motd.d/ 2>/dev/null\n" +
		"fi\n" +
		"if [ -f /etc/bash_completion ]; then\n" +
		"\t. /etc/bash_completion\n" +
		"fi\n"

	got, changed := stripVscodeMotd(content)
	if !changed {
		t.Fatal("应识别出老版本片段")
	}

	want := "# /etc/bash.bashrc\n" +
		"if [ -f /etc/bash_completion ]; then\n" +
		"\t. /etc/bash_completion\n" +
		"fi\n"
	if got != want {
		t.Fatalf("相邻的 if 块被破坏\n得到:\n%q\n期望:\n%q", got, want)
	}
}

func TestStripVscodeMotdRemovesPairedBlock(t *testing.T) {
	content := "export PATH=$PATH:/usr/local/bin\n\n" + vscodeMotdSnippet

	got, changed := stripVscodeMotd(content)
	if !changed {
		t.Fatal("应识别出成对标记的片段")
	}
	if got != "export PATH=$PATH:/usr/local/bin\n" {
		t.Fatalf("删除结果不正确: %q", got)
	}
}

func TestStripVscodeMotdKeepsUnrelatedContent(t *testing.T) {
	content := "if [ -f /etc/bash_completion ]; then\n\t. /etc/bash_completion\nfi\n"

	got, changed := stripVscodeMotd(content)
	if changed {
		t.Fatal("没有标记时不应改动文件")
	}
	if got != content {
		t.Fatalf("内容被改动: %q", got)
	}
}

// 找不到结束边界时宁可留着片段，也不能把后面的内容一路删掉。
func TestStripVscodeMotdLeavesUnterminatedBlockAlone(t *testing.T) {
	content := vscodeMotdBeginMarker + "\nif [ -n \"$X\" ]; then\n\techo hi\n"

	got, changed := stripVscodeMotd(content)
	if changed {
		t.Fatal("缺少 end 标记时不应改动文件")
	}
	if got != content {
		t.Fatalf("内容被改动: %q", got)
	}
}

// 反复 set 不应堆积重复片段或空行。
func TestStripThenInjectIsIdempotent(t *testing.T) {
	base := "# /etc/bash.bashrc\n"

	content, _ := stripVscodeMotd(base)
	content = strings.TrimRight(content, "\n") + "\n\n" + vscodeMotdSnippet

	again, _ := stripVscodeMotd(content)
	again = strings.TrimRight(again, "\n") + "\n\n" + vscodeMotdSnippet

	if again != content {
		t.Fatalf("重复注入结果不稳定\n第一次:\n%q\n第二次:\n%q", content, again)
	}
	if strings.Count(again, vscodeMotdBeginMarker) != 1 {
		t.Fatalf("片段重复注入: %q", again)
	}
}

// 片段必须直接调用 motd render：走 run-parts 会在 apt 恢复默认脚本可执行位后
// 让 VSCode 终端里同时冒出 Ubuntu 默认 MOTD。
func TestVscodeMotdSnippetCallsRenderDirectly(t *testing.T) {
	if strings.Contains(vscodeMotdSnippet, "run-parts") {
		t.Error("片段不应再调用 run-parts")
	}
	if !strings.Contains(vscodeMotdSnippet, installedBinPath+" motd render") {
		t.Errorf("片段应直接调用 motd render: %q", vscodeMotdSnippet)
	}
	if !strings.HasSuffix(vscodeMotdSnippet, vscodeMotdEndMarker+"\n") {
		t.Errorf("片段应以 end 标记收尾: %q", vscodeMotdSnippet)
	}
}
