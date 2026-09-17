package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot 定位到本仓库根目录。测试的工作目录是 cmd/gokit。
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("定位仓库根目录失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("%s 下没有 go.mod，定位错了: %v", root, err)
	}
	return root
}

func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("PATH 上没有 %s，跳过；这个测试在完整工具链下必须实际运行", name)
	}
}

// TestNewGeneratesBuildableProject 是这个脚手架的验收标准：
// 生成出来的项目不需要人再动一行，就能构建。
//
// 它用 --replace 指向工作区里的框架，因为要验的是当前代码，
// 不是已经发布的那个 tag。
func TestNewGeneratesBuildableProject(t *testing.T) {
	if testing.Short() {
		t.Skip("要真的跑构建，慢")
	}
	requireTool(t, "go")
	requireTool(t, "wire")
	requireTool(t, "gofmt")

	proj := filepath.Join(t.TempDir(), "myapp")

	var out bytes.Buffer
	code := runNew(&out, []string{
		proj,
		"--mod", "example.com/myapp",
		"--replace", repoRoot(t),
	})
	if code != 0 {
		t.Fatalf("gokit new 退出码 = %d，输出：\n%s", code, out.String())
	}

	// 装配文件必须是生成好的，而不是留给使用者自己去跑。
	if _, err := os.Stat(filepath.Join(proj, "cmd", "server", "wire_gen.go")); err != nil {
		t.Fatalf("没有生成 wire_gen.go: %v\n%s", err, out.String())
	}

	build := exec.Command("go", "build", "./...")
	build.Dir = proj
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("生成的项目构建失败: %v\n%s", err, b)
	}

	vet := exec.Command("go", "vet", "./...")
	vet.Dir = proj
	if b, err := vet.CombinedOutput(); err != nil {
		t.Fatalf("生成的项目 go vet 失败: %v\n%s", err, b)
	}

	// 生成的代码要求 gofmt 干净：每个用脚手架起的项目都会带着这份格式，
	// 第一次有人跑 gofmt -w 就不该冒出一堆无关 diff。
	gofmtCmd := exec.Command("gofmt", "-l", ".")
	gofmtCmd.Dir = proj
	gofmtOut, err := gofmtCmd.Output()
	if err != nil {
		t.Fatalf("gofmt -l 执行失败: %v", err)
	}
	if dirty := strings.TrimSpace(string(gofmtOut)); dirty != "" {
		t.Errorf("生成的项目不是 gofmt 干净的，以下文件需要重新格式化：\n%s", dirty)
	}
}

// TestNewGeneratesGofmtCleanProjectForNonExampleModulePath 专门盯字母序：
// 其它生成测试全用 example.com/myapp，"e" 排在 "github.com" 前面，
// 模板里但凡有一处把本项目 import 和第三方 import 硬编码在同一组、顺序写死，
// 都会被这个巧合掩盖过去。这里换一个字母序排在 github.com 之后的域名，
// 专门用来戳穿这类问题。用 --skip-tools 只落盘、不跑构建，保持这条用例快。
func TestNewGeneratesGofmtCleanProjectForNonExampleModulePath(t *testing.T) {
	requireTool(t, "gofmt")

	proj := filepath.Join(t.TempDir(), "myapp")

	if code := runNew(io.Discard, []string{proj, "--mod", "gitlab.com/myorg/myapp", "--skip-tools"}); code != 0 {
		t.Fatalf("gokit new 退出码 != 0")
	}

	gofmtCmd := exec.Command("gofmt", "-l", ".")
	gofmtCmd.Dir = proj
	gofmtOut, err := gofmtCmd.Output()
	if err != nil {
		t.Fatalf("gofmt -l 执行失败: %v", err)
	}
	if dirty := strings.TrimSpace(string(gofmtOut)); dirty != "" {
		t.Errorf("生成的项目不是 gofmt 干净的，以下文件需要重新格式化：\n%s", dirty)
	}
}

// TestNewGeneratesLayerCleanProject 把 doctor 掉头指向自己的产物：
// 脚手架生成的东西必须自己守得住分层规则。
func TestNewGeneratesLayerCleanProject(t *testing.T) {
	proj := filepath.Join(t.TempDir(), "myapp")

	if code := runNew(io.Discard, []string{proj, "--mod", "example.com/myapp", "--skip-tools"}); code != 0 {
		t.Fatalf("gokit new 退出码 = %d", code)
	}

	result, err := CheckLayers(proj)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	// CheckLayers 在 internal/ 不存在、或者一个符合形状的文件都没识别到时，
	// 同样会返回零值 Violations + nil error。只看 Violations == 0 拦不住
	// 模板退化成不产出任何文件——必须确认它真的检查过东西。
	if result.FilesChecked == 0 {
		t.Fatalf("CheckLayers 一个文件都没识别到，这个断言等于没跑")
	}
	// 解析失败的文件不会出现在 Violations 里，而是单独放在 ParseFailures——
	// 模板产出语法不合法的 Go 源码时，Violations 仍可能是空的。
	if len(result.ParseFailures) != 0 {
		t.Errorf("生成的项目里有文件解析失败：%v", result.ParseFailures)
	}
	if len(result.Violations) != 0 {
		t.Errorf("脚手架生成的项目自己就违反了分层规则：%v", result.Violations)
	}
}

func TestNewRefusesExistingNonEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "占位.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if code := runNew(&out, []string{dir, "--mod", "example.com/myapp", "--skip-tools"}); code == 0 {
		t.Error("往非空目录里生成应该被拒绝，实际成功了")
	}
}

func TestNewRequiresModulePath(t *testing.T) {
	var out bytes.Buffer
	if code := runNew(&out, []string{filepath.Join(t.TempDir(), "myapp")}); code == 0 {
		t.Error("缺 --mod 应该报错，实际成功了")
	}
}

func TestNewRejectsInvalidModuleName(t *testing.T) {
	var out bytes.Buffer
	dst := filepath.Join(t.TempDir(), "myapp")
	code := runNew(&out, []string{dst, "--mod", "example.com/myapp", "--module", "user-profile", "--skip-tools"})
	if code == 0 {
		t.Error("--module user-profile 不是合法的 Go 标识符，应该被拒绝，实际成功了")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("--module 校验失败时不应该已经落盘")
	}
}

func TestNewRejectsModPathWithWhitespace(t *testing.T) {
	var out bytes.Buffer
	dst := filepath.Join(t.TempDir(), "myapp")
	code := runNew(&out, []string{dst, "--mod", "example.com/my app", "--skip-tools"})
	if code == 0 {
		t.Error("--mod 包含空白字符应该被拒绝，实际成功了")
	}
}
