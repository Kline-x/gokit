package main

import (
	"bytes"
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
