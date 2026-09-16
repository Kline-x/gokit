# 脚手架 CLI 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 一条命令生成一个能立刻构建、立刻跑起来的 gokit 项目，并能检查出分层规则的违反。

**Architecture:** `cmd/gokit` 是库模块里的一个 `main` 包，只用标准库。模板以 `embed.FS` 打进二进制，用 `text/template` 渲染。三条命令：`new` 生成项目，`wire` 封装代码生成，`doctor` 检查工具链与依赖方向。

**Tech Stack:** 标准库的 `flag`、`embed`、`text/template`、`go/parser`、`os/exec`。**不引入任何第三方依赖。**

对应设计文档：`docs/superpowers/specs/2026-09-15-gokit-framework-design.md` 第 11 节与第 12 节第 6 步。
前三份计划已完成并合并，框架已发布 `v0.1.0`。

---

## Global Constraints

这一节适用于**每一个** Task。

1. **Go 版本**：库模块 `go.mod` 是 `go 1.22`，**不得改动**。
2. **`cmd/gokit` 只允许标准库。** 它和库是同一个模块，任何依赖都会落进使用者的 `go.mod`。所以没有 cobra、没有 x/tools。若你认为某件事非第三方库不可，**停下来报告**。
3. **模块路径** `github.com/Kline-x/gokit`。仓库在 `E:\code\AI\vibCoding\gokit`，WSL 内 `/mnt/e/code/AI/vibCoding/gokit`。
4. **所有 go / wire 命令在 WSL 中执行**：

   ```bash
   wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go test ./cmd/gokit -race -count=1'
   ```

   Go 在 `/home/gaore/sdk/go/bin/go`，`wire` 在 `/home/gaore/go/bin/wire`。**不要用裸的 `$HOME`**，会被静默吞掉；`PATH=` 赋值要加引号，因为继承来的 Windows PATH 里有空格。命令跑完后打印的 `chdir(...) failed` 是中继产物，看退出码。若 Git Bash 改写了 `/home/gaore/...` 路径，命令前加 `MSYS_NO_PATHCONV=1`。
5. **模板文件用 `.tmpl` 后缀**，放在 `cmd/gokit/template/` 下，这样它们不会被当成 Go 源码编译。
6. **文档、目录名、注释、提交信息中不出现 "DDD" 字样**，统一说「分层」「领域模型」。
7. **注释与提交信息用中文**。提交用 `git -c user.name=xuyang -c user.email=xuyang@89you.com commit`，信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。**这行是字面文本，执行者不要换成自己的模型名。**
8. **不改框架已有的包**。`app`、`config`、`transport`、`component/*` 一律不动。若发现必须改，**停下来报告**——那是个值得单独决定的发现。
9. **每个 Task 结束时提交一次**，提交前 `go vet ./...` 与 `go test ./... -race` 必须通过，示例模块也要照旧通过。

---

## 范围决定（先说清楚，免得来回）

**生成的是服务项目，不是 CLI 项目。** HTTP 服务总是生成，数据库与 gRPC 由参数决定。一个不需要 HTTP 的命令行项目是另一种形状，留给将来的 `--kind cli`，本计划不做。删掉不需要的部分比让模板长满条件分支便宜。

**参数而不是交互。** 设计文档说「交互只问一件事」，本计划改成 `--sql` / `--grpc` 两个开关。理由是可脚本化、可测试；真要交互，将来在参数缺省时补一个提问即可，不影响现在的形状。

**生成的项目引用 `v0.1.0`。** 框架开发时用 `--replace <path>` 指向本地检出，CLI 自己的测试也走这条路——它要测的是工作区里的框架，不是已发布的那个。

**`gokit new` 会调用 `wire`。** 生成的项目需要 `wire_gen.go` 才能构建。`wire` 不在 PATH 上时，`new` 仍然写出所有文件，然后告诉使用者去跑 `gokit wire`，而不是假装成功。

**不做 `gokit new module`。** 往既有项目里加模块是另一件事，它要解析既有的 `wire.go` 并改写，风险和工作量都独立。另立计划。

---

## File Structure

| 文件 | 职责 |
|---|---|
| `cmd/gokit/main.go` | 子命令分发与用法 |
| `cmd/gokit/doctor.go` | `doctor` 命令：工具链检查 |
| `cmd/gokit/layers.go` | 依赖方向检查，`doctor` 用，可单独测 |
| `cmd/gokit/layers_test.go` | 依赖方向检查的测试 |
| `cmd/gokit/new.go` | `new` 命令：参数、渲染、落盘、调 wire |
| `cmd/gokit/new_test.go` | 生成即构建的验收测试 |
| `cmd/gokit/wire.go` | `wire` 命令 |
| `cmd/gokit/render.go` | 模板遍历与渲染 |
| `cmd/gokit/template/**/*.tmpl` | 项目模板 |

---

## Task 1: CLI 骨架与 doctor 的工具链检查

**Files:**
- Create: `cmd/gokit/main.go`, `cmd/gokit/doctor.go`, `cmd/gokit/doctor_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `func main()`，支持 `gokit <command> [flags]`，未知命令与 `-h` 都打印用法
  - `type check struct { name string; ok bool; detail string }`
  - `func runDoctor(w io.Writer, args []string) int`
  - `func toolchainChecks() []check`

- [ ] **Step 1: 写失败的测试**

`cmd/gokit/doctor_test.go`：

```go
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
```

- [ ] **Step 2: 跑测试确认失败**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go test ./cmd/gokit -v'
```

预期编译失败：`undefined: runDoctor`。

- [ ] **Step 3: 写 cmd/gokit/main.go**

```go
// Command gokit 是 gokit 框架的脚手架。
//
// 它只用标准库：这个二进制与框架库同属一个模块，引入任何第三方依赖
// 都会落进使用者的 go.mod，而使用者要的是框架，不是脚手架的依赖树。
package main

import (
	"fmt"
	"os"
)

const usage = `gokit 是 gokit 框架的脚手架。

用法：
  gokit new <项目目录> --mod <模块路径> [--sql] [--grpc] [--replace <本地框架路径>]
        生成一个能立刻构建的新项目。

  gokit wire [目录]
        在项目里跑一遍代码生成，然后构建一次确认结果可用。默认当前目录。

  gokit doctor [目录]
        检查工具链是否齐备，以及分层依赖方向有没有被破坏。默认当前目录。

每条命令加 -h 看它自己的参数。
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "new":
		os.Exit(runNew(os.Stdout, os.Args[2:]))
	case "wire":
		os.Exit(runWire(os.Stdout, os.Args[2:]))
	case "doctor":
		os.Exit(runDoctor(os.Stdout, os.Args[2:]))
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usage)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "未知命令 %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}
```

`runNew` 与 `runWire` 要到 Task 3 与 Task 5 才存在。为了让本任务能编译，**现在先在 `main.go` 里放两个占位实现**，后续任务再换掉：

```go
// runNew 由 Task 3 实现。
func runNew(w io.Writer, args []string) int {
	fmt.Fprintln(w, "gokit new 尚未实现")
	return 2
}

// runWire 由 Task 5 实现。
func runWire(w io.Writer, args []string) int {
	fmt.Fprintln(w, "gokit wire 尚未实现")
	return 2
}
```

补上 `"io"` 的 import。这两个占位在它们各自的任务里会被删掉，别忘了。

- [ ] **Step 4: 写 cmd/gokit/doctor.go**

```go
package main

import (
	"flag"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// minGoMinor 是框架要求的 Go 次版本下限。标准库的方法路由是 1.22 才有的。
const minGoMinor = 22

// check 是一项诊断结果。
type check struct {
	// name 是这项检查的名字，例如「Go 版本」。
	name string
	// ok 表示这一项是否通过。
	ok bool
	// detail 给出具体情况，通过与否都要写清楚，好让人知道看到的是什么。
	detail string
}

// runDoctor 执行诊断。
//
// 工具缺失只如实报告，不判失败——缺 protoc 的项目多得是，它们照样能跑。
// 只有分层依赖方向被破坏才返回非零，因为那是代码本身出了问题。
func runDoctor(w io.Writer, args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(w)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}

	fmt.Fprintln(w, "工具链")
	for _, c := range toolchainChecks() {
		printCheck(w, c)
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "分层依赖方向")
	violations, err := CheckLayers(dir)
	if err != nil {
		printCheck(w, check{name: "依赖方向", ok: false, detail: err.Error()})
		return 1
	}
	if len(violations) == 0 {
		printCheck(w, check{name: "依赖方向", ok: true, detail: "没有发现违反"})
		return 0
	}
	for _, v := range violations {
		printCheck(w, check{name: "依赖方向", ok: false, detail: v.String()})
	}
	return 1
}

func printCheck(w io.Writer, c check) {
	mark := "✓"
	if !c.ok {
		mark = "✗"
	}
	fmt.Fprintf(w, "  %s %s：%s\n", mark, c.name, c.detail)
}

// toolchainChecks 检查生成代码需要的几样东西。
func toolchainChecks() []check {
	return []check{
		goVersionCheck(),
		binaryCheck("wire", "代码装配生成器，gokit wire 要用它"),
		binaryCheck("protoc", "proto 编译器，只有用 gRPC 才需要"),
		binaryCheck("protoc-gen-go", "protoc 的 Go 插件"),
		binaryCheck("protoc-gen-go-grpc", "protoc 的 gRPC 插件"),
	}
}

func goVersionCheck() check {
	v := runtime.Version() // 形如 go1.27.1
	minor, ok := goMinor(v)
	if !ok {
		return check{name: "Go 版本", ok: false, detail: "认不出版本号 " + v}
	}
	if minor < minGoMinor {
		return check{
			name: "Go 版本",
			ok:   false,
			detail: fmt.Sprintf("%s，低于要求的 1.%d", v, minGoMinor),
		}
	}
	return check{name: "Go 版本", ok: true, detail: v}
}

// goMinor 从 go1.27.1 这样的字符串里取出次版本号。
func goMinor(v string) (int, bool) {
	rest, ok := strings.CutPrefix(v, "go1.")
	if !ok {
		return 0, false
	}
	if i := strings.IndexByte(rest, '.'); i >= 0 {
		rest = rest[:i]
	}
	n, err := strconv.Atoi(rest)
	if err != nil {
		return 0, false
	}
	return n, true
}

func binaryCheck(name, purpose string) check {
	path, err := exec.LookPath(name)
	if err != nil {
		return check{name: name, ok: false, detail: "没找到。" + purpose}
	}
	return check{name: name, ok: true, detail: path}
}
```

`CheckLayers` 与 `violation.String()` 由 Task 2 提供。为了让本任务编译，**先在 `doctor.go` 末尾放一个占位**，Task 2 会把它换成真实实现并搬进 `layers.go`：

```go
// CheckLayers 由 Task 2 实现。
func CheckLayers(dir string) ([]violation, error) { return nil, nil }

type violation struct{}

func (v violation) String() string { return "" }
```

- [ ] **Step 5: 跑测试确认通过**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go test ./cmd/gokit -v -race -count=1'
```

三个测试全过。

- [ ] **Step 6: 跑全量并提交**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
```

```bash
git add cmd/gokit
git commit -m "脚手架：命令骨架与 doctor 的工具链检查"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 2: doctor 的依赖方向检查

这是 `doctor` 真正值钱的部分：把设计文档里那四条分层规则变成能跑的检查。

**Files:**
- Create: `cmd/gokit/layers.go`, `cmd/gokit/layers_test.go`
- Modify: `cmd/gokit/doctor.go`（删掉占位）

**Interfaces:**
- Consumes: 无
- Produces:
  - `type violation struct { File string; Layer string; Import string; Rule string }`
  - `func (v violation) String() string`
  - `func CheckLayers(root string) ([]violation, error)`

- [ ] **Step 1: 写失败的测试**

`cmd/gokit/layers_test.go`：

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeGo 在 root 下按相对路径写一个只有 import 的 Go 文件。
func writeGo(t *testing.T, root, rel string, imports ...string) {
	t.Helper()

	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}

	var b strings.Builder
	b.WriteString("package p\n\nimport (\n")
	for _, imp := range imports {
		b.WriteString("\t\"" + imp + "\"\n")
	}
	b.WriteString(")\n")

	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
}

func TestCheckLayersAcceptsCleanTree(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "internal/user/domain/user.go", "context", "errors")
	writeGo(t, root, "internal/user/application/service.go",
		"context", "example.com/app/internal/user/domain")
	writeGo(t, root, "internal/user/infrastructure/repo.go",
		"database/sql", "github.com/Kline-x/gokit/component/sqldb",
		"example.com/app/internal/user/domain")
	writeGo(t, root, "internal/user/interfaces/http.go",
		"net/http", "example.com/app/internal/user/application")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("干净的树不该有违反，实际 = %v", got)
	}
}

func TestCheckLayersCatchesDomainImportingOutside(t *testing.T) {
	root := t.TempDir()
	// domain 只能依赖标准库。
	writeGo(t, root, "internal/user/domain/user.go",
		"context", "github.com/Kline-x/gokit/component/sqldb")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("违反数 = %d, want 1：%v", len(got), got)
	}
	if !strings.Contains(got[0].String(), "sqldb") {
		t.Errorf("违反信息里没点出是哪个 import：%s", got[0])
	}
}

func TestCheckLayersCatchesApplicationImportingInfrastructure(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "internal/user/application/service.go",
		"example.com/app/internal/user/infrastructure")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("违反数 = %d, want 1：%v", len(got), got)
	}
}

func TestCheckLayersCatchesDomainImportingSiblingModule(t *testing.T) {
	root := t.TempDir()
	// 兄弟模块也不行，哪怕只是它的 domain。
	writeGo(t, root, "internal/user/domain/user.go",
		"example.com/app/internal/order/domain")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("违反数 = %d, want 1：%v", len(got), got)
	}
}

func TestCheckLayersCatchesInterfacesImportingInfrastructure(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "internal/user/interfaces/http.go",
		"example.com/app/internal/user/infrastructure")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("违反数 = %d, want 1：%v", len(got), got)
	}
}

func TestCheckLayersAllowsCrossModuleApplicationImport(t *testing.T) {
	root := t.TempDir()
	// 跨模块调用只能走对方的 application 接口，这一条是允许的。
	writeGo(t, root, "internal/order/application/service.go",
		"example.com/app/internal/user/application")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("跨模块引用对方 application 是允许的，实际 = %v", got)
	}
}

func TestCheckLayersIgnoresNonLayerDirectories(t *testing.T) {
	root := t.TempDir()
	// cmd 与 pkg 不在四层里，不受这套规则约束。
	writeGo(t, root, "cmd/server/main.go",
		"example.com/app/internal/user/infrastructure")
	writeGo(t, root, "pkg/util/util.go",
		"example.com/app/internal/user/domain")
	// internal 之下也有不属于四层的东西：remote 是与 infrastructure 并列的
	// 出站适配器，module.go 是模块的装配声明。这两样都不受四层规则约束。
	writeGo(t, root, "internal/user/remote/client.go",
		"example.com/app/internal/user/infrastructure")
	writeGo(t, root, "internal/user/module.go",
		"example.com/app/internal/user/infrastructure")
	// 让这棵树里确实有 internal 目录，否则 CheckLayers 会早早返回，
	// 上面几条根本没被走到——那样这个测试就是假绿的。
	writeGo(t, root, "internal/user/domain/user.go", "context")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("四层之外的目录不该被判违反，实际 = %v", got)
	}
}

func TestCheckLayersOnMissingDirectoryIsNotAnError(t *testing.T) {
	// 一个还没有 internal 的目录不算出错，只是没东西可查。
	got, err := CheckLayers(t.TempDir())
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("空目录不该有违反，实际 = %v", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go test ./cmd/gokit -run TestCheckLayers -v'
```

预期大量失败：占位的 `CheckLayers` 永远返回空。

- [ ] **Step 3: 写 cmd/gokit/layers.go**

```go
package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// violation 是一处对分层依赖方向的违反。
type violation struct {
	// File 是出问题的文件，相对于被检查的根目录。
	File string
	// Layer 是这个文件所属的层，例如 domain。
	Layer string
	// Import 是不该出现的那条 import。
	Import string
	// Rule 用一句话说明违反了哪条规则。
	Rule string
}

func (v violation) String() string {
	return fmt.Sprintf("%s（%s 层）不该 import %s —— %s", v.File, v.Layer, v.Import, v.Rule)
}

// 四层的名字。目录名必须正好是这几个之一才受约束。
const (
	layerDomain         = "domain"
	layerApplication    = "application"
	layerInfrastructure = "infrastructure"
	layerInterfaces     = "interfaces"
)

// CheckLayers 遍历 root/internal 下的业务模块，检查分层依赖方向。
//
// 规则来自设计文档，逐条对应：
//  1. domain 只能依赖标准库，也不能 import 兄弟模块。
//  2. application 只能 import 本模块 domain，以及别的模块的 application。
//  3. infrastructure 与 interfaces 互不依赖。
//  4. 跨模块调用只能走对方的 application。
//
// 判断「是不是标准库」用的是「路径第一段里有没有点」这个惯例：
// 标准库的导入路径没有域名，第三方的有。这条惯例在 Go 里一直成立。
//
// root/internal 不存在时不算出错，只是没东西可查——刚起步的项目就是这样。
func CheckLayers(root string) ([]violation, error) {
	internal := filepath.Join(root, "internal")
	info, err := os.Stat(internal)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("gokit: 检查 %s 失败: %w", internal, err)
	}
	if !info.IsDir() {
		return nil, nil
	}

	var violations []violation

	walkErr := filepath.WalkDir(internal, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}

		module, layer, ok := moduleAndLayer(internal, path)
		if !ok {
			return nil
		}

		imports, parseErr := importsOf(path)
		if parseErr != nil {
			return parseErr
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		for _, imp := range imports {
			if rule, bad := judge(layer, module, imp); bad {
				violations = append(violations, violation{
					File: rel, Layer: layer, Import: imp, Rule: rule,
				})
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("gokit: 遍历 %s 失败: %w", internal, walkErr)
	}

	return violations, nil
}

// moduleAndLayer 从文件路径里认出它属于哪个模块的哪一层。
//
// 认的是 internal/<模块>/<层>/... 这个形状；层名不在四层之内的一律不管，
// 例如 internal/user/remote 或 internal/user/module.go。
func moduleAndLayer(internal, path string) (module, layer string, ok bool) {
	rel, err := filepath.Rel(internal, path)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 3 {
		return "", "", false
	}
	switch parts[1] {
	case layerDomain, layerApplication, layerInfrastructure, layerInterfaces:
		return parts[0], parts[1], true
	default:
		return "", "", false
	}
}

// importsOf 只解析 import 段，不读函数体。
func importsOf(path string) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("gokit: 解析 %s 失败: %w", path, err)
	}

	out := make([]string, 0, len(f.Imports))
	for _, spec := range f.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// judge 判断某一层引用某个包是否违规。返回违反的规则说明。
func judge(layer, module, imp string) (rule string, bad bool) {
	if isStdlib(imp) {
		return "", false
	}

	otherModule, otherLayer, inside := parseInternalImport(imp)

	switch layer {
	case layerDomain:
		// 规则 1：domain 只能依赖标准库。
		return "domain 只能依赖标准库", true

	case layerApplication:
		if !inside {
			// 引用框架或第三方，允许——事务能力之类的抽象要落在这里。
			return "", false
		}
		if otherModule == module {
			if otherLayer == layerDomain {
				return "", false
			}
			return "application 只能 import 本模块的 domain", true
		}
		// 规则 4：跨模块只能走对方的 application。
		if otherLayer == layerApplication {
			return "", false
		}
		return "跨模块调用只能走对方的 application 层", true

	case layerInfrastructure:
		if inside && otherModule == module && otherLayer == layerInterfaces {
			return "infrastructure 与 interfaces 互不依赖", true
		}
		if inside && otherModule != module && otherLayer != layerApplication {
			return "跨模块调用只能走对方的 application 层", true
		}
		return "", false

	case layerInterfaces:
		if inside && otherModule == module && otherLayer == layerInfrastructure {
			return "interfaces 与 infrastructure 互不依赖", true
		}
		if inside && otherModule != module && otherLayer != layerApplication {
			return "跨模块调用只能走对方的 application 层", true
		}
		return "", false
	}

	return "", false
}

// isStdlib 按惯例判断：标准库的导入路径第一段里没有点。
func isStdlib(imp string) bool {
	first, _, _ := strings.Cut(imp, "/")
	return !strings.Contains(first, ".")
}

// parseInternalImport 认出一条指向某个业务模块某一层的 import。
//
// 认的是路径里出现 internal/<模块>/<层> 的形状，不关心前面的模块路径是什么，
// 这样检查就不必知道项目自己的 module path。
func parseInternalImport(imp string) (module, layer string, ok bool) {
	parts := strings.Split(imp, "/")
	for i, p := range parts {
		if p != "internal" {
			continue
		}
		if i+2 >= len(parts) {
			// internal/<模块> 到此为止，没有层。
			if i+1 < len(parts) {
				return parts[i+1], "", true
			}
			return "", "", false
		}
		return parts[i+1], parts[i+2], true
	}
	return "", "", false
}
```

- [ ] **Step 4: 删掉 doctor.go 里的占位**

把 Task 1 末尾加的这三行从 `cmd/gokit/doctor.go` 删掉：

```go
// CheckLayers 由 Task 2 实现。
func CheckLayers(dir string) ([]violation, error) { return nil, nil }

type violation struct{}

func (v violation) String() string { return "" }
```

- [ ] **Step 5: 跑测试确认通过**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go test ./cmd/gokit -v -race -count=1'
```

`layers_test.go` 的八个测试与 Task 1 的三个全过。

**若某条规则的测试挂了，先确认是实现错了还是规则理解错了，再改。** 这八条是设计文档四条规则的逐条翻译，改测试之前先回去读那四条。

- [ ] **Step 6: 拿真实的示例树验一遍**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go run ./cmd/gokit doctor example/minimal'
```

示例是按这套分层写的，**依赖方向那一节必须报没有违反**。若它报出违反，那要么是检查过严，要么是示例真的破了规则——两种都值得停下来报告，不要为了让它通过而放宽规则。

- [ ] **Step 7: 跑全量并提交**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
```

```bash
git add cmd/gokit
git commit -m "脚手架：把分层依赖方向变成能跑的检查"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 3: 模板机制与 gokit new（HTTP + 数据库）

这是本计划的主体。验收标准只有一句：**生成出来的项目能构建**。

**Files:**
- Create: `cmd/gokit/render.go`, `cmd/gokit/new.go`, `cmd/gokit/new_test.go`
- Create: `cmd/gokit/template/**`（见下）
- Modify: `cmd/gokit/main.go`（删掉 `runNew` 占位）

**Interfaces:**
- Consumes: 无
- Produces:
  - `type projectData struct { ModPath, Name, Module, ModuleTitle, GokitVersion, Replace string; WithSQL, WithGRPC bool }`
  - `func render(dst string, data projectData) error`
  - `func runNew(w io.Writer, args []string) int`

### 模板从哪里来：抄 example/minimal，不要自己发明

`example/minimal` 已经是这个形状，而且是被两个测试盯住的、确认正确的形状。**模板是它的简化版，不是重写。**

具体对应关系：

| 模板文件 | 抄自 | 怎么简化 |
|---|---|---|
| `cmd/server/main.go.tmpl` | `example/minimal/cmd/monolith/main.go` | 去掉 gRPC 相关的 provider（Task 4 再加回来） |
| `cmd/server/wire.go.tmpl` | `example/minimal/cmd/monolith/wire.go` | 同上；用 `{{.Module}}.LocalSet` |
| `internal/__module__/module.go.tmpl` | `example/minimal/internal/greeter/module.go` | 只留 `LocalSet` |
| `internal/__module__/domain/*.tmpl` | `example/minimal/internal/greeter/domain/` | 保留实体与仓储接口，业务逻辑换成最简单的 |
| `internal/__module__/application/*.tmpl` | `.../application/` | 保留 `Service` 接口与实现 |
| `internal/__module__/infrastructure/*.tmpl` | `.../infrastructure/` | 保留 SQL 仓储与建表 |
| `internal/__module__/interfaces/*.tmpl` | `.../interfaces/http.go` | 只留 HTTP |
| `configs/config.yaml.tmpl` | `example/minimal/configs/` | 去掉 gRPC 段 |

**动手前先把 `example/minimal` 完整读一遍。** 不读就写，写出来的分层大概率是错的，而 `gokit doctor` 会当场抓到——那时返工更贵。

其余几个文件没有设计含义，自己写即可：`.gitignore` 模板（忽略二进制、`*.db`、`.env`）、`Makefile.tmpl`（`build` / `run` / `test` / `wire` 四个目标）、`README.md.tmpl`（怎么跑起来、目录各层是干什么的、改了装配要跑 `gokit wire`）。

### 路径占位

模板目录里那一级业务模块目录**字面就叫 `__module__`**。渲染时把路径里的 `__module__` 替换成实际模块名，文件内容里则用 `{{.Module}}`。不用 `{{}}` 做目录名，因为 Windows 文件名不允许这些字符。

所有模板文件带 `.tmpl` 后缀，落盘时去掉。

- [ ] **Step 1: 写失败的验收测试**

`cmd/gokit/new_test.go`：

```go
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

	violations, err := CheckLayers(proj)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("脚手架生成的项目自己就违反了分层规则：%v", violations)
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
```

注意 `--skip-tools`：不跑 `go mod tidy` 与 `wire`，只落盘。没有它，那几个快测试都要连网。

- [ ] **Step 2: 跑测试确认失败**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go test ./cmd/gokit -run TestNew -v'
```

预期全挂：占位的 `runNew` 永远返回 2。

- [ ] **Step 3: 读 example/minimal**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && find example/minimal -name "*.go" -not -name "*_test.go" -not -name "wire_gen.go" | sort'
```

至少读完 `cmd/monolith/{main,wire}.go`、`internal/greeter/module.go` 和四层各自的文件。同时记下 `example/minimal/go.mod` 里的 `go` 指令与 SQLite 驱动版本，模板要用同一套。

- [ ] **Step 4: 写 cmd/gokit/render.go**

```go
package main

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed all:template
var templates embed.FS

// modulePlaceholder 是模板目录里代表业务模块名的那一级目录。
// 用它而不是模板语法，因为花括号不能出现在 Windows 文件名里。
const modulePlaceholder = "__module__"

// projectData 是模板的渲染数据。
type projectData struct {
	// ModPath 是生成项目的 go.mod 模块路径。
	ModPath string
	// Name 是应用名，取模块路径的最后一段。
	Name string
	// Module 是示例业务模块的名字，小写。
	Module string
	// ModuleTitle 是 Module 首字母大写后的形式，用于类型名。
	ModuleTitle string
	// GokitVersion 是生成项目依赖的框架版本。
	GokitVersion string
	// Replace 非空时，生成的 go.mod 里加一条指向本地框架的 replace。
	// 框架自身开发时用得到，普通使用者用不上。
	Replace string
	// WithSQL 决定是否生成数据库与仓储实现。
	WithSQL bool
	// WithGRPC 决定是否生成 gRPC 服务端与 proto。
	WithGRPC bool
}

// render 把内嵌模板渲染到 dst。
//
// 空文件会被跳过：模板里用整文件的条件来开关一个文件时，渲染结果就是空的，
// 落一个空 .go 文件下去会让项目编译不过。
func render(dst string, data projectData) error {
	return fs.WalkDir(templates, "template", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		rel := strings.TrimPrefix(path, "template/")
		rel = strings.ReplaceAll(rel, modulePlaceholder, data.Module)
		rel = strings.TrimSuffix(rel, ".tmpl")
		if filepath.Base(rel) == "gitignore" {
			rel = filepath.Join(filepath.Dir(rel), ".gitignore")
		}
		out := filepath.Join(dst, filepath.FromSlash(rel))

		src, err := templates.ReadFile(path)
		if err != nil {
			return fmt.Errorf("gokit: 读模板 %s 失败: %w", path, err)
		}

		tpl, err := template.New(rel).Option("missingkey=error").Parse(string(src))
		if err != nil {
			return fmt.Errorf("gokit: 解析模板 %s 失败: %w", path, err)
		}

		var buf strings.Builder
		if err := tpl.Execute(&buf, data); err != nil {
			return fmt.Errorf("gokit: 渲染模板 %s 失败: %w", path, err)
		}

		content := strings.TrimLeft(buf.String(), "\n")
		if strings.TrimSpace(content) == "" {
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return fmt.Errorf("gokit: 建目录失败: %w", err)
		}
		if err := os.WriteFile(out, []byte(content), 0o644); err != nil {
			return fmt.Errorf("gokit: 写 %s 失败: %w", out, err)
		}
		return nil
	})
}
```

两处容易漏的：

1. `all:template` 前缀不能省——不带它，`embed` 会跳过以点开头的文件。
2. `.gitignore` 在模板里存成 `gitignore.tmpl`，由上面那段改名。直接叫 `.gitignore.tmpl` 有被仓库自身忽略规则挡在版本控制之外的风险。加完后 **`git status --short cmd/gokit/template` 确认它真的进了暂存区**。

- [ ] **Step 5: 写模板**

按上面的对应表，从 `example/minimal` 派生。目录结构：

```
cmd/gokit/template/
├── go.mod.tmpl
├── gitignore.tmpl
├── Makefile.tmpl
├── README.md.tmpl
├── configs/config.yaml.tmpl
├── cmd/server/main.go.tmpl
├── cmd/server/wire.go.tmpl
└── internal/__module__/
    ├── module.go.tmpl
    ├── domain/__module__.go.tmpl
    ├── application/service.go.tmpl
    ├── infrastructure/repository.go.tmpl
    └── interfaces/http.go.tmpl
```

`go.mod.tmpl` 大致形状（版本与 `go` 指令照抄 `example/minimal/go.mod`）：

```
module {{.ModPath}}

go 1.25.0

require (
	github.com/Kline-x/gokit {{.GokitVersion}}
{{- if .WithSQL}}
	modernc.org/sqlite v1.xx.x
{{- end}}
)
{{if .Replace}}
replace github.com/Kline-x/gokit => {{.Replace}}
{{end}}
```

`go` 指令是 1.25.0 而不是框架的 1.22，原因在 SQLite 驱动，不在 gokit。**在 `README.md.tmpl` 里写明这一点**，否则使用者会以为是框架的要求。不带 `--sql` 时这个下限应该降回 1.22——用 `{{if .WithSQL}}` 控制 `go` 指令那一行。

`--replace` 给的是本地路径，模板里直接插入。Windows 路径含反斜杠，写进 `go.mod` 会出问题：**在 `runNew` 里先 `filepath.ToSlash`。**

- [ ] **Step 6: 写 cmd/gokit/new.go**

```go
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

// defaultGokitVersion 是生成项目默认依赖的框架版本。
const defaultGokitVersion = "v0.1.0"

func runNew(w io.Writer, args []string) int {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	fs.SetOutput(w)

	modPath := fs.String("mod", "", "go.mod 的模块路径，必填，例如 example.com/myapp")
	module := fs.String("module", "hello", "生成的示例业务模块名")
	version := fs.String("gokit-version", defaultGokitVersion, "依赖的 gokit 版本")
	replace := fs.String("replace", "", "指向本地 gokit 检出的路径，框架开发时用")
	withSQL := fs.Bool("sql", true, "生成数据库组件与仓储实现")
	withGRPC := fs.Bool("grpc", false, "生成 gRPC 服务端与 proto")
	skipTools := fs.Bool("skip-tools", false, "只落盘，不跑 go mod tidy 与 wire")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(w, "用法：gokit new <项目目录> --mod <模块路径>")
		return 2
	}
	dst := fs.Arg(0)

	if *modPath == "" {
		fmt.Fprintln(w, "gokit: --mod 是必填的，它决定生成项目的 import 路径")
		return 2
	}
	if err := checkEmpty(dst); err != nil {
		fmt.Fprintf(w, "gokit: %v\n", err)
		return 1
	}

	data := projectData{
		ModPath:      *modPath,
		Name:         lastSegment(*modPath),
		Module:       *module,
		ModuleTitle:  title(*module),
		GokitVersion: *version,
		Replace:      filepath.ToSlash(*replace),
		WithSQL:      *withSQL,
		WithGRPC:     *withGRPC,
	}

	if err := render(dst, data); err != nil {
		fmt.Fprintf(w, "gokit: %v\n", err)
		return 1
	}
	fmt.Fprintf(w, "已生成 %s\n", dst)

	if *skipTools {
		fmt.Fprintln(w, "跳过了 go mod tidy 与 wire，记得自己跑一遍 gokit wire")
		return 0
	}

	// 顺序不能反：wire 要能解析依赖才能生成，所以先 tidy。
	if err := runIn(dst, "go", "mod", "tidy"); err != nil {
		fmt.Fprintf(w, "gokit: %v\n", err)
		return 1
	}
	if err := runIn(dst, "wire", "./..."); err != nil {
		// wire 装不上不该让生成前功尽弃：文件都在，补跑一次就行。
		fmt.Fprintf(w, "gokit: 代码生成没跑成：%v\n", err)
		fmt.Fprintf(w, "文件已经生成好了，装上 wire 之后在 %s 里跑 gokit wire 即可。\n", dst)
		fmt.Fprintln(w, "  go install github.com/google/wire/cmd/wire@latest")
		return 1
	}

	fmt.Fprintf(w, "\n好了。下一步：\n  cd %s\n  go run ./cmd/server\n", dst)
	return 0
}

// checkEmpty 确认目标目录不存在或为空。往有东西的目录里生成会覆盖别人的文件。
func checkEmpty(dst string) error {
	entries, err := os.ReadDir(dst)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取 %s 失败: %w", dst, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s 不是空目录，换一个位置", dst)
	}
	return nil
}

func runIn(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s 失败: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	return nil
}

func lastSegment(modPath string) string {
	if i := strings.LastIndexByte(modPath, '/'); i >= 0 {
		return modPath[i+1:]
	}
	return modPath
}

// title 把 hello 变成 Hello，用于生成类型名。
func title(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
```

删掉 `main.go` 里的 `runNew` 占位。

- [ ] **Step 7: 跑测试**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && PATH="/home/gaore/sdk/go/bin:/home/gaore/go/bin:$PATH" && go test ./cmd/gokit -v -race -count=1 -timeout 300s'
```

**`TestNewGeneratesBuildableProject` 必须显示 PASS，不能是 SKIP。** 出现 SKIP 说明 `go` 或 `wire` 没在 PATH 上，把上面的 `PATH=` 补对再跑——跳过的验收测试等于没有验收。

头一遍多半会失败在模板的编译错误上。**去看 `go build` 的报错，一条条修模板**，不要改测试。

- [ ] **Step 8: 亲手跑一遍，看看生成的东西**

```bash
wsl -u gaore bash -lc 'PATH="/home/gaore/sdk/go/bin:/home/gaore/go/bin:$PATH" && rm -rf /tmp/gokit-try && cd /tmp && go run /mnt/e/code/AI/vibCoding/gokit/cmd/gokit new gokit-try --mod example.com/try --replace /mnt/e/code/AI/vibCoding/gokit && find gokit-try -type f | sort'
```

然后真的把它跑起来，确认不是「能编译但跑不了」：

```bash
wsl -u gaore bash -lc 'PATH="/home/gaore/sdk/go/bin:$PATH" && cd /tmp/gokit-try && (go run ./cmd/server &) && sleep 3 && curl -sS -i localhost:8080/ping ; sleep 1 ; pkill -f "cmd/server" || true'
```

**在报告里贴出实际的 HTTP 响应。** 端口与路径以模板里实际写的为准，对不上就照改。

- [ ] **Step 9: 跑全量并提交**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && PATH="/home/gaore/sdk/go/bin:/home/gaore/go/bin:$PATH" && go vet ./... && go test ./... -race -count=1 -timeout 300s'
```

```bash
git status --short cmd/gokit/template
git add cmd/gokit
git commit -m "脚手架：gokit new 生成一个能直接构建运行的项目"
```

第一条命令是用来确认 `.gitignore` 模板没被仓库自己的忽略规则挡住的。提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 4: --grpc 变体

**Files:**
- Modify: `cmd/gokit/template/**`（加 gRPC 条件分支与新文件）
- Modify: `cmd/gokit/new_test.go`

**Interfaces:**
- Consumes: Task 3 的 `render` 与 `projectData`
- Produces: 无新的导出符号，只多一条能走通的参数组合

- [ ] **Step 1: 补失败的测试**

往 `cmd/gokit/new_test.go` 里加：

```go
// TestNewWithGRPCGeneratesBuildableProject 验的是第二条参数路径。
// 它单独存在而不是并进上一个测试，因为两者失败时要能一眼看出是哪条路径断了。
func TestNewWithGRPCGeneratesBuildableProject(t *testing.T) {
	if testing.Short() {
		t.Skip("要真的跑构建，慢")
	}
	requireTool(t, "go")
	requireTool(t, "wire")

	proj := filepath.Join(t.TempDir(), "myapp")

	var out bytes.Buffer
	code := runNew(&out, []string{
		proj, "--mod", "example.com/myapp", "--grpc", "--replace", repoRoot(t),
	})
	if code != 0 {
		t.Fatalf("gokit new --grpc 退出码 = %d，输出：\n%s", code, out.String())
	}

	build := exec.Command("go", "build", "./...")
	build.Dir = proj
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("带 gRPC 的项目构建失败: %v\n%s", err, b)
	}
}

// TestNewWithoutSQLGeneratesBuildableProject 覆盖第三条：不要数据库。
func TestNewWithoutSQLGeneratesBuildableProject(t *testing.T) {
	if testing.Short() {
		t.Skip("要真的跑构建，慢")
	}
	requireTool(t, "go")
	requireTool(t, "wire")

	proj := filepath.Join(t.TempDir(), "myapp")

	var out bytes.Buffer
	code := runNew(&out, []string{
		proj, "--mod", "example.com/myapp", "--sql=false", "--replace", repoRoot(t),
	})
	if code != 0 {
		t.Fatalf("gokit new --sql=false 退出码 = %d，输出：\n%s", code, out.String())
	}

	build := exec.Command("go", "build", "./...")
	build.Dir = proj
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("不带数据库的项目构建失败: %v\n%s", err, b)
	}
}
```

顺带把 `TestNewGeneratesLayerCleanProject` 改成对四种参数组合都查一遍分层（表驱动：`--sql` 开关 × `--grpc` 开关）。

- [ ] **Step 2: 跑测试确认失败**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && PATH="/home/gaore/sdk/go/bin:/home/gaore/go/bin:$PATH" && go test ./cmd/gokit -run "TestNewWith" -v'
```

- [ ] **Step 3: 补 gRPC 模板**

需要动的地方：

1. **新文件** `api/__module__/v1/__module__.proto.tmpl`——抄 `example/minimal/api/greeter/v1/`，把服务与消息改成对应模板模块。
2. **新文件** `internal/__module__/interfaces/grpc.go.tmpl`——抄 `example/minimal/internal/greeter/interfaces/grpc.go`。整个文件包在 `{{if .WithGRPC}}...{{end}}` 里，不选 gRPC 时渲染成空，由 `render` 跳过。
3. **改** `cmd/server/{main,wire}.go.tmpl` 与 `configs/config.yaml.tmpl`——用 `{{if .WithGRPC}}` 加上 gRPC 服务端的 provider、配置段与组件注册。
4. **改** `go.mod.tmpl`——`{{if .WithGRPC}}` 时加 grpc 与 protobuf 的 require，版本**照抄 `example/minimal/go.mod`**（grpc v1.65.0、protobuf v1.35.2 是特意钉住的，别换成 latest，那会把 `go` 指令顶上去）。
5. **改** `Makefile.tmpl`——加一个 `proto` 目标。

`.pb.go` 不生成、不内嵌。proto 是源文件，编译产物由使用者跑 `make proto` 得到。

**于是有个问题要处理：**带 `--grpc` 生成的项目，`interfaces/grpc.go` 会 import 还不存在的 `api/.../v1` 包，`go build` 直接失败——而上面那个测试要求它能构建。

两种解法，**选第一种**：

- **`runNew` 在 `--grpc` 且 `protoc` 可用时，自动跑一遍 protoc 生成 `.pb.go`**，与跑 `wire` 是同一个性质的事。`protoc` 不可用时和 wire 一样：文件都留着，提示使用者装了再跑 `make proto`，返回非零。测试里相应加 `requireTool(t, "protoc")`。
- （不选）让 proto 生成的代码也进模板。那等于把 protoc 的产物签进脚手架，版本一错就全错。

protoc 的调用参数照抄 `example/minimal/Makefile` 里的那条，别自己拼。

- [ ] **Step 4: 跑测试确认通过**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && PATH="/home/gaore/sdk/go/bin:/home/gaore/go/bin:/home/gaore/bin:$PATH" && go test ./cmd/gokit -v -race -count=1 -timeout 600s'
```

`/home/gaore/bin` 是 protoc 所在，这次要加进去。**四条参数路径的构建测试都必须 PASS 而不是 SKIP。**

- [ ] **Step 5: 亲手验一遍 gRPC 那条**

生成一个带 gRPC 的项目，起进程，用 `grpcurl` 或一小段客户端代码打一次，**贴出实际响应**。没有 `grpcurl` 就在临时目录里写个几行的客户端跑一次。

- [ ] **Step 6: 跑全量并提交**

```bash
git add cmd/gokit
git commit -m "脚手架：支持生成带 gRPC 的项目"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 5: gokit wire

改了装配之后要重新生成，这条命令把「跑 wire 再构建一次确认结果可用」合成一步。它很薄，但省掉的是「生成完了没构建，下次运行才发现坏了」。

**Files:**
- Create: `cmd/gokit/wire.go`, `cmd/gokit/wire_test.go`
- Modify: `cmd/gokit/main.go`（删掉 `runWire` 占位）

**Interfaces:**
- Consumes: Task 3 的 `runIn`
- Produces: `func runWire(w io.Writer, args []string) int`

- [ ] **Step 1: 写失败的测试**

`cmd/gokit/wire_test.go`：

```go
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
```

- [ ] **Step 2: 跑测试确认失败**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && PATH="/home/gaore/sdk/go/bin:/home/gaore/go/bin:$PATH" && go test ./cmd/gokit -run TestWire -v'
```

- [ ] **Step 3: 写 cmd/gokit/wire.go**

```go
package main

import (
	"flag"
	"fmt"
	"io"
)

// runWire 重新生成装配代码，然后构建一次。
//
// 多跑的那次构建不是多余的：wire 生成成功不等于结果能编译——
// 比如提供者签名变了但调用方没跟上。不当场发现，就要等到下次运行才发现。
func runWire(w io.Writer, args []string) int {
	fs := flag.NewFlagSet("wire", flag.ContinueOnError)
	fs.SetOutput(w)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}

	if err := runIn(dir, "wire", "./..."); err != nil {
		fmt.Fprintf(w, "gokit: 生成装配代码失败：%v\n", err)
		return 1
	}
	fmt.Fprintln(w, "装配代码已重新生成")

	if err := runIn(dir, "go", "build", "./..."); err != nil {
		fmt.Fprintf(w, "gokit: 生成后构建不通过：%v\n", err)
		return 1
	}
	fmt.Fprintln(w, "构建通过")
	return 0
}
```

删掉 `main.go` 里的 `runWire` 占位。

注意 `runIn` 已经把子进程的合并输出拼进了 error，所以 `%v` 会带出 wire 自己的报错——第二个测试查的就是这件事。

- [ ] **Step 4: 跑测试确认通过，跑全量，提交**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && PATH="/home/gaore/sdk/go/bin:/home/gaore/go/bin:/home/gaore/bin:$PATH" && go vet ./... && go test ./... -race -count=1 -timeout 600s'
```

```bash
git add cmd/gokit
git commit -m "脚手架：gokit wire 重新生成装配并当场验证"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 6: 文档回填

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-09-15-gokit-framework-design.md`

- [ ] **Step 1: 改 README.md**

三处：

1. 「本版不包含」里删掉「脚手架 CLI」——它现在有了。
2. 「包一览」上面加一节 **「起一个新项目」**：装 CLI（`go install github.com/Kline-x/gokit/cmd/gokit@v0.1.0`）、`gokit new myapp --mod example.com/myapp`、进去 `go run ./cmd/server`。写清楚生成的项目依赖 `wire`（`--grpc` 时还要 `protoc` 与两个插件），以及 `gokit doctor` 能一次性告诉你缺什么。
3. 「包一览」表格里加一行 `cmd/gokit`。

顺手说明 `gokit doctor` 不只是查工具：它会检查分层依赖方向，**这是把设计约束变成能跑的检查**，可以直接进 CI。

- [ ] **Step 2: 改设计文档**

`docs/superpowers/specs/2026-09-15-gokit-framework-design.md` 第 12 节，把第 6 步标成已完成，格式与已标好的第 1–5、7 步一致。

若第 11 节写的形状与最终实现有出入（比如那里写的是交互式提问，实现是参数开关；那里写了 `gokit new module`，本计划没做），**在第 11 节补一小段说明实际做成了什么、哪些留到后续**，不要让设计文档和代码对不上。

- [ ] **Step 3: 同步到 E:\ai-md**

把本计划同步一份到 `E:\ai-md\claude\plan\`，中文命名，例如 `gokit脚手架CLI实施计划.md`。README 与设计文档属于仓库活文档，**不搬运**。

- [ ] **Step 4: 提交**

```bash
git add README.md docs
git commit -m "文档：补上脚手架 CLI 的用法与设计对照"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## 验收

全部 Task 完成后，这几条要同时成立：

1. `go vet ./...` 与 `go test ./... -race` 全绿，且 `cmd/gokit` 的四条生成路径测试是 **PASS 不是 SKIP**
2. `gokit new` 的四种参数组合生成的项目都能 `go build` 与 `go vet`
3. `gokit doctor` 对 `example/minimal` 和所有生成的项目都报「没有发现违反」
4. `gokit doctor` 对人为破坏分层的树能报出违反并返回非零
5. 库模块的 `go` 指令仍是 `go 1.22`，且 **`go.mod` 的 require 段没有新增任何第三方依赖**
6. 生成的项目真的能跑起来并响应请求（HTTP 与 gRPC 各验一次）

第 5 条尤其要当场核一下：`git diff master -- go.mod` 应该是空的。
