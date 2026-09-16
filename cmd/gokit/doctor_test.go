package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorPrintsEveryCheck(t *testing.T) {
	var buf bytes.Buffer
	code := runDoctor(&buf, nil)

	out := buf.String()
	for _, want := range []string{"Go 版本", "wire", "protoc"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出里没有 %q 这一项：\n%s", want, out)
		}
	}
	// doctor 是诊断命令：工具缺失要如实报告，但不该让调用方以为出了故障。
	// 只有依赖方向被破坏才算失败，那是 Task 2 的事。
	if code != 0 {
		t.Errorf("退出码 = %d, want 0（工具缺失只报告，不判失败）", code)
	}
}

func TestDoctorMarksResults(t *testing.T) {
	var buf bytes.Buffer
	runDoctor(&buf, nil)

	out := buf.String()
	if !strings.Contains(out, "✓") && !strings.Contains(out, "✗") {
		t.Errorf("每一项都该有明确的通过或未通过标记：\n%s", out)
	}
}

// 没有 internal 目录的树上跑 doctor，输出必须说明「未检查任何文件」，不能
// 和「查过且干净」共用同一句「没有发现违反」——那样使用者看到的绿灯可能
// 只是因为目录形状没对上，根本没检查任何文件。
func TestDoctorReportsZeroFilesChecked(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	code := runDoctor(&buf, []string{dir})

	out := buf.String()
	if !strings.Contains(out, "未检查任何文件") {
		t.Errorf("没有 internal 目录时，输出里应该说明未检查任何文件：\n%s", out)
	}
	if code != 0 {
		t.Errorf("退出码 = %d, want 0（没检查任何文件不算失败）", code)
	}
}

// 有文件解析失败时，doctor 要照实报出来，且退出码不能是 0。
func TestDoctorReportsParseFailuresAndFailsNonZero(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "internal/order/application/broken.go")
	if err := os.MkdirAll(filepath.Dir(badPath), 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	if err := os.WriteFile(badPath, []byte("这不是合法的 Go 代码 {{{"), 0o600); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}

	var buf bytes.Buffer
	code := runDoctor(&buf, []string{dir})

	if code == 0 {
		t.Errorf("有解析失败的文件时退出码不该是 0")
	}
	if !strings.Contains(buf.String(), "解析失败") {
		t.Errorf("输出里应该报出解析失败的文件：\n%s", buf.String())
	}
}

func TestGoVersionCheckAcceptsCurrentToolchain(t *testing.T) {
	// 本仓库的测试就跑在符合要求的工具链上，这一项必须是通过的。
	for _, c := range toolchainChecks() {
		if strings.Contains(c.name, "Go 版本") {
			if !c.ok {
				t.Errorf("Go 版本检查判定为未通过：%s", c.detail)
			}
			return
		}
	}
	t.Fatal("没找到 Go 版本这一项检查")
}
