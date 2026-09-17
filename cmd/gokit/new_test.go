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

// assertGoBuildable 是四条构建路径共用的验收：生成的项目不需要人再动一行就能用。
// 装配文件（wire_gen.go）必须是生成好的，构建、vet、gofmt 都要干净。
func assertGoBuildable(t *testing.T, proj string) {
	t.Helper()

	// 装配文件必须是生成好的，而不是留给使用者自己去跑。
	if _, err := os.Stat(filepath.Join(proj, "cmd", "server", "wire_gen.go")); err != nil {
		t.Fatalf("没有生成 wire_gen.go: %v", err)
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

// assertGRPCNotGenerated 确认没开 --grpc 时，gRPC 相关产物一个都不该出现。
// 条件渲染一旦退化成「总是生成」，四条构建测试全绿也发现不了——它们只验证
// 了开着的时候文件在，从没验证过关着的时候文件不在。
func assertGRPCNotGenerated(t *testing.T, proj, module string) {
	t.Helper()

	if _, err := os.Stat(filepath.Join(proj, "internal", module, "interfaces", "grpc.go")); err == nil {
		t.Error("没有 --grpc 时不应该生成 internal/.../interfaces/grpc.go")
	}
	if _, err := os.Stat(filepath.Join(proj, "api")); err == nil {
		t.Error("没有 --grpc 时不应该生成 api/ 目录")
	}
}

// assertGRPCGenerated 确认 --grpc 真的生成了 gRPC 接口层与编译产物，
// 而不是一个不消费 WithGRPC 的空 flag：开与不开生成结果不该完全一样。
func assertGRPCGenerated(t *testing.T, proj, module string) {
	t.Helper()

	if _, err := os.Stat(filepath.Join(proj, "internal", module, "interfaces", "grpc.go")); err != nil {
		t.Fatalf("没有生成 gRPC 接口层 interfaces/grpc.go: %v", err)
	}
	if _, err := os.Stat(filepath.Join(proj, "api", module, "v1", module+".proto")); err != nil {
		t.Fatalf("没有生成 proto 源文件: %v", err)
	}
	pbFiles, err := filepath.Glob(filepath.Join(proj, "api", module, "v1", "*.pb.go"))
	if err != nil {
		t.Fatalf("查找 .pb.go 失败: %v", err)
	}
	if len(pbFiles) == 0 {
		t.Fatalf("没有生成 .pb.go，protoc 没有自动跑成")
	}
}

// TestNewGeneratesBuildableProject 是这个脚手架的验收标准之一，覆盖第一条参数
// 路径（--sql 默认开着，不带 --grpc）：生成出来的项目不需要人再动一行，就能构建。
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

	assertGoBuildable(t, proj)
	assertGRPCNotGenerated(t, proj, "hello")
}

// TestNewWithGRPCGeneratesBuildableProject 覆盖第二条参数路径：--sql 默认开着，
// 加 --grpc。它单独存在而不是并进上一个测试，因为两者失败时要能一眼看出是
// 哪条路径断了。
//
// 需要 protoc：--grpc 生成的 interfaces/grpc.go 会 import 还不存在的
// api/.../v1 包，runNew 内部会在 --grpc 时自动跑一遍 protoc 生成 .pb.go，
// 与跑 wire 是同一个性质的事。
func TestNewWithGRPCGeneratesBuildableProject(t *testing.T) {
	if testing.Short() {
		t.Skip("要真的跑构建，慢")
	}
	requireTool(t, "go")
	requireTool(t, "wire")
	requireTool(t, "protoc")
	requireTool(t, "gofmt")

	proj := filepath.Join(t.TempDir(), "myapp")

	var out bytes.Buffer
	code := runNew(&out, []string{
		proj, "--mod", "example.com/myapp", "--grpc", "--replace", repoRoot(t),
	})
	if code != 0 {
		t.Fatalf("gokit new --grpc 退出码 = %d，输出：\n%s", code, out.String())
	}

	assertGRPCGenerated(t, proj, "hello")
	assertGoBuildable(t, proj)
}

// TestNewWithoutSQLGeneratesBuildableProject 覆盖第三条参数路径：不要数据库，
// 也不要 gRPC。
func TestNewWithoutSQLGeneratesBuildableProject(t *testing.T) {
	if testing.Short() {
		t.Skip("要真的跑构建，慢")
	}
	requireTool(t, "go")
	requireTool(t, "wire")
	requireTool(t, "gofmt")

	proj := filepath.Join(t.TempDir(), "myapp")

	var out bytes.Buffer
	code := runNew(&out, []string{
		proj, "--mod", "example.com/myapp", "--sql=false", "--replace", repoRoot(t),
	})
	if code != 0 {
		t.Fatalf("gokit new --sql=false 退出码 = %d，输出：\n%s", code, out.String())
	}

	assertGoBuildable(t, proj)
}

// TestNewWithoutSQLAndWithGRPCGeneratesBuildableProject 覆盖第四条参数路径：
// 不要数据库，但要 gRPC。四条路径里最容易被漏掉的一条，因为它同时关掉
// WithSQL 涉及的所有条件分支、又打开 WithGRPC 涉及的所有条件分支。
func TestNewWithoutSQLAndWithGRPCGeneratesBuildableProject(t *testing.T) {
	if testing.Short() {
		t.Skip("要真的跑构建，慢")
	}
	requireTool(t, "go")
	requireTool(t, "wire")
	requireTool(t, "protoc")
	requireTool(t, "gofmt")

	proj := filepath.Join(t.TempDir(), "myapp")

	var out bytes.Buffer
	code := runNew(&out, []string{
		proj, "--mod", "example.com/myapp", "--sql=false", "--grpc", "--replace", repoRoot(t),
	})
	if code != 0 {
		t.Fatalf("gokit new --sql=false --grpc 退出码 = %d，输出：\n%s", code, out.String())
	}

	assertGRPCGenerated(t, proj, "hello")
	assertGoBuildable(t, proj)
}

// TestNewWithGRPCWhenProtocMissingKeepsFilesAndPrintsRemedy 覆盖降级路径：
// protoc 不在 PATH 上时，--grpc 不能静默退化——文件要留着、退出码要非零、
// 提示里要有完整的补救步骤，不然某天这条分支要是被改成了 return 0，
// 使用者拿到的会是一个看起来生成成功、实际编译不过的项目，且查不出原因。
//
// 摘掉整个 PATH 就够了：runProtoc 失败会在跑 go mod tidy、wire 之前就
// return，所以不需要 PATH 上还留着 go/wire，也不用担心它们被误跑到。
func TestNewWithGRPCWhenProtocMissingKeepsFilesAndPrintsRemedy(t *testing.T) {
	t.Setenv("PATH", "")

	proj := filepath.Join(t.TempDir(), "myapp")

	var out bytes.Buffer
	code := runNew(&out, []string{proj, "--mod", "example.com/myapp", "--grpc"})
	if code == 0 {
		t.Fatalf("protoc 缺失时应该返回非零退出码，实际是 0，输出：\n%s", out.String())
	}

	if _, err := os.Stat(filepath.Join(proj, "api", "hello", "v1", "hello.proto")); err != nil {
		t.Errorf("protoc 缺失时 .proto 源文件应该留着：%v", err)
	}
	if _, err := os.Stat(filepath.Join(proj, "internal", "hello", "interfaces", "grpc.go")); err != nil {
		t.Errorf("protoc 缺失时 grpc.go 应该留着：%v", err)
	}

	msg := out.String()
	for _, want := range []string{"protoc", "make tools", "make proto", "go mod tidy", "make wire"} {
		if !strings.Contains(msg, want) {
			t.Errorf("提示里应该包含补救步骤 %q，实际输出：\n%s", want, msg)
		}
	}
}

// TestNewGeneratesGofmtCleanProjectForNonExampleModulePath 专门盯字母序：
// 其它生成测试全用 example.com/myapp，"e" 排在 "github.com" 前面，
// 模板里但凡有一处把本项目 import 和第三方 import 硬编码在同一组、顺序写死，
// 都会被这个巧合掩盖过去。这里换一个字母序排在 github.com 之后的域名，
// 专门用来戳穿这类问题。用 --skip-tools 只落盘、不跑构建，保持这条用例快。
//
// 表驱动覆盖 --sql × --grpc 四种组合：--grpc 生成的 interfaces/grpc.go 里，
// 同时有本项目的 api/.../v1、application 两个 import 和第三方的
// google.golang.org/grpc，正是这类顺序问题最容易藏身的地方。
func TestNewGeneratesGofmtCleanProjectForNonExampleModulePath(t *testing.T) {
	requireTool(t, "gofmt")

	cases := []struct {
		name string
		args []string
	}{
		{"sql默认_no_grpc", nil},
		{"sql默认_grpc", []string{"--grpc"}},
		{"no_sql_no_grpc", []string{"--sql=false"}},
		{"no_sql_grpc", []string{"--sql=false", "--grpc"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proj := filepath.Join(t.TempDir(), "myapp")

			args := append([]string{proj, "--mod", "gitlab.com/myorg/myapp", "--skip-tools"}, tc.args...)
			if code := runNew(io.Discard, args); code != 0 {
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
		})
	}
}

// TestNewGeneratesLayerCleanProject 把 doctor 掉头指向自己的产物：
// 脚手架生成的东西必须自己守得住分层规则。表驱动覆盖 --sql × --grpc 四种组合。
func TestNewGeneratesLayerCleanProject(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"sql默认_no_grpc", nil},
		{"sql默认_grpc", []string{"--grpc"}},
		{"no_sql_no_grpc", []string{"--sql=false"}},
		{"no_sql_grpc", []string{"--sql=false", "--grpc"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proj := filepath.Join(t.TempDir(), "myapp")

			args := append([]string{proj, "--mod", "example.com/myapp", "--skip-tools"}, tc.args...)
			if code := runNew(io.Discard, args); code != 0 {
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
		})
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
