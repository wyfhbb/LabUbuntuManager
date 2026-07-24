package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseMirrorList(t *testing.T) {
	got, err := parseMirrorList(" https://docker.1ms.run/ , https://a.example.com\nhttps://docker.1ms.run ")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	want := []string{"https://docker.1ms.run", "https://a.example.com"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("得到 %v，期望 %v（应去掉尾斜杠并去重）", got, want)
	}
}

func TestParseMirrorListRejectsBadInput(t *testing.T) {
	for _, raw := range []string{"", "   ", "docker.1ms.run", "ftp://mirror.example.com"} {
		if _, err := parseMirrorList(raw); err == nil {
			t.Errorf("parseMirrorList(%q) 应返回错误", raw)
		}
	}
}

func TestDefaultDockerMirrorsAreValid(t *testing.T) {
	mirrors, err := parseMirrorList(defaultDockerMirrorsRaw)
	if err != nil {
		t.Fatalf("内置默认镜像地址不合法: %v", err)
	}
	if len(mirrors) == 0 {
		t.Fatal("内置默认镜像地址为空")
	}
}

func TestMergeRegistryMirrorsPreservesOtherKeys(t *testing.T) {
	existing := []byte(`{
  "log-driver": "json-file",
  "data-root": "/data/docker",
  "registry-mirrors": ["https://old.example.com"]
}`)

	out, err := mergeRegistryMirrors(existing, []string{"https://docker.1ms.run"})
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}

	var conf map[string]any
	if err := json.Unmarshal(out, &conf); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, out)
	}
	if conf["log-driver"] != "json-file" {
		t.Errorf("log-driver 丢失: %v", conf["log-driver"])
	}
	if conf["data-root"] != "/data/docker" {
		t.Errorf("data-root 丢失: %v", conf["data-root"])
	}

	mirrors := currentRegistryMirrors(out)
	if len(mirrors) != 1 || mirrors[0] != "https://docker.1ms.run" {
		t.Errorf("registry-mirrors 未被替换: %v", mirrors)
	}
}

func TestMergeRegistryMirrorsOnEmptyFile(t *testing.T) {
	out, err := mergeRegistryMirrors(nil, []string{"https://docker.1ms.run"})
	if err != nil {
		t.Fatalf("空文件应视为空对象: %v", err)
	}

	mirrors := currentRegistryMirrors(out)
	if len(mirrors) != 1 || mirrors[0] != "https://docker.1ms.run" {
		t.Errorf("registry-mirrors 未写入: %v", mirrors)
	}
	if !strings.HasSuffix(string(out), "\n") {
		t.Error("输出应以换行结尾")
	}
}

func TestMergeRegistryMirrorsRejectsBrokenJSON(t *testing.T) {
	if _, err := mergeRegistryMirrors([]byte("{ 这不是 JSON"), []string{"https://x.example.com"}); err == nil {
		t.Fatal("现有配置无法解析时应返回错误，而不是覆盖掉它")
	}
}
