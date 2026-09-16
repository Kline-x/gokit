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
			name:   "Go 版本",
			ok:     false,
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
