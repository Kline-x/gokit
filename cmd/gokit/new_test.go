package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
