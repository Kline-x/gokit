package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWireRegeneratesAfterAssemblyChange 验的是这条命令真正的用途：
// 改了 wire.go 之后，跑一次就能把 wire_gen.go 追上。
func TestWireRegeneratesAfterAssemblyChange(t *testing.T) {
	if testing.Short() {
		t.Skip("要真的跑代码生成与构建，慢")
	}
	requireTool(t, "go")
	requireTool(t, "wire")

	proj := filepath.Join(t.TempDir(), "myapp")
	var out bytes.Buffer
	if code := runNew(&out, []string{proj, "--mod", "example.com/myapp", "--replace", repoRoot(t)}); code != 0 {
		t.Fatalf("准备项目失败，退出码 %d：\n%s", code, out.String())
	}

	gen := filepath.Join(proj, "cmd", "server", "wire_gen.go")
	before, err := os.ReadFile(gen)
	if err != nil {
		t.Fatalf("读 wire_gen.go 失败: %v", err)
	}

	// 把生成结果毁掉，看 gokit wire 能不能把它修回来。
	if err := os.Remove(gen); err != nil {
		t.Fatalf("删 wire_gen.go 失败: %v", err)
	}

	out.Reset()
	if code := runWire(&out, []string{proj}); code != 0 {
		t.Fatalf("gokit wire 退出码 = %d：\n%s", code, out.String())
	}

	after, err := os.ReadFile(gen)
	if err != nil {
		t.Fatalf("gokit wire 之后 wire_gen.go 还是不在: %v", err)
	}
	if string(before) != string(after) {
		t.Error("重新生成的结果和原来不一致，说明生成不是幂等的")
	}
}

// TestWireReportsBrokenAssembly 验的是它会不会把失败咽下去。
func TestWireReportsBrokenAssembly(t *testing.T) {
	if testing.Short() {
		t.Skip("要真的跑代码生成，慢")
	}
	requireTool(t, "go")
	requireTool(t, "wire")

	proj := filepath.Join(t.TempDir(), "myapp")
	var out bytes.Buffer
	if code := runNew(&out, []string{proj, "--mod", "example.com/myapp", "--replace", repoRoot(t)}); code != 0 {
		t.Fatalf("准备项目失败，退出码 %d：\n%s", code, out.String())
	}

	// 往装配里塞一个谁也提供不了的依赖，wire 必须报错。
	wireGo := filepath.Join(proj, "cmd", "server", "wire.go")
	src, err := os.ReadFile(wireGo)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(src), "wire.Build(", "wire.Build(\n\t\tprovideNothing,", 1)
	if broken == string(src) {
		t.Fatal("没找到 wire.Build，模板形状变了，这个测试要跟着改")
	}
	if err := os.WriteFile(wireGo, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if code := runWire(&out, []string{proj}); code == 0 {
		t.Errorf("装配坏了，gokit wire 却报成功：\n%s", out.String())
	}
	if !strings.Contains(out.String(), "provideNothing") {
		t.Errorf("报错信息里没带上 wire 自己的输出，排查不了：\n%s", out.String())
	}
}

// TestWireReportsBuildFailureAfterSuccessfulGeneration 验的是 gokit wire
// 相对 make wire（只跑 wire 生成）的全部增量：wire 生成成功、但生成结果编不
// 过时，也必须报错，而不是把这次失败咽下去当成成功。
//
// wire.go 带 //go:build wireinject，wire 加载装配时只看得见它；反过来在
// cmd/server 下放一个带 //go:build !wireinject 的文件、里面写一处类型错误，
// wire 生成阶段看不到它（能正常生成），但 go build ./... 不带这个 tag，
// 会编到它、报类型错误——这样就把「wire 生成成功」和「随后 build 失败」
// 这两件事在同一次调用里都构造出来了，覆盖了两个现有测试都没走到的分支。
func TestWireReportsBuildFailureAfterSuccessfulGeneration(t *testing.T) {
	if testing.Short() {
		t.Skip("要真的跑代码生成与构建，慢")
	}
	requireTool(t, "go")
	requireTool(t, "wire")

	proj := filepath.Join(t.TempDir(), "myapp")
	var out bytes.Buffer
	if code := runNew(&out, []string{proj, "--mod", "example.com/myapp", "--replace", repoRoot(t)}); code != 0 {
		t.Fatalf("准备项目失败，退出码 %d：\n%s", code, out.String())
	}

	broken := "//go:build !wireinject\n\npackage main\n\nvar _ int = \"类型错误\"\n"
	brokenPath := filepath.Join(proj, "cmd", "server", "only_in_build.go")
	if err := os.WriteFile(brokenPath, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	code := runWire(&out, []string{proj})
	if code == 0 {
		t.Fatalf("生成后构建不通过，gokit wire 却报成功：\n%s", out.String())
	}
	if !strings.Contains(out.String(), "生成后构建不通过") {
		t.Errorf("输出里应该说明是构建这一步失败的，实际：\n%s", out.String())
	}
}
