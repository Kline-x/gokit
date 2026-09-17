package main

import (
	"errors"
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

// minGoMinorSQL 是带 --sql（默认开启）生成的项目要求的 Go 次版本下限。
// 这不是 gokit 框架本身的要求，而是示例用的 SQLite 驱动
// modernc.org/sqlite 需要更新的 Go 版本。
const minGoMinorSQL = 25

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
	requireFiles := fs.Bool("require-files", false,
		"一个文件都没检查到时返回非零退出码（默认关）；接进 CI 的回归护栏用它防止目录形状变化后检查静默空跑")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
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
	result, err := CheckLayers(dir)
	if err != nil {
		printCheck(w, check{name: "依赖方向", ok: false, detail: err.Error()})
		return 1
	}
	if len(result.Violations) == 0 && len(result.ParseFailures) == 0 {
		// 「一个文件都没查」和「查过且干净」在输出上必须分得开，否则使用者
		// 看到的绿灯可能只是因为目录形状没对上、根本没检查任何文件。
		if result.FilesChecked == 0 {
			detail := "没有找到 internal/<模块>/<层> 结构，未检查任何文件"
			if *requireFiles {
				// --require-files 打开时，「一个文件都没查」不再算通过——
				// 接进 CI 的回归护栏原本是靠某个固定目录今天恰好有文件撑着，
				// 目录形状一旦变化，护栏会从「查过且干净」静默退化成
				// 「什么都没查」，这个开关就是把这类退化钉成显式失败。
				printCheck(w, check{name: "依赖方向", ok: false, detail: detail + "（--require-files 要求至少检查到一个文件）"})
				return 1
			}
			printCheck(w, check{name: "依赖方向", ok: true, detail: detail})
			return 0
		}
		printCheck(w, check{name: "依赖方向", ok: true, detail: fmt.Sprintf("检查了 %d 个文件，没有发现违反", result.FilesChecked)})
		return 0
	}
	for _, v := range result.Violations {
		printCheck(w, check{name: "依赖方向", ok: false, detail: v.String()})
	}
	for _, pf := range result.ParseFailures {
		printCheck(w, check{name: "依赖方向", ok: false, detail: pf.String()})
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
		binaryCheck("go", "Go 工具链，new 与 wire 都要调用它"),
		binaryCheck("wire", "代码装配生成器，gokit wire 要用它"),
		binaryCheck("protoc", "proto 编译器，只有用 gRPC 才需要"),
		binaryCheck("protoc-gen-go", "protoc 的 Go 插件"),
		binaryCheck("protoc-gen-go-grpc", "protoc 的 gRPC 插件"),
	}
}

func goVersionCheck() check {
	v, ok := goVersionFromPATH()
	// fallbackNote 只在真正走了兜底分支时才非空，追加在 detail 末尾说明
	// 这个版本号的真实来源——不然单看这一行的数字，会误以为 PATH 上装的
	// 就是这个版本，实际它是编译 gokit 这个二进制所用的工具链版本，
	// 与 gokit new/wire 实际会调用的那个 go 可能完全不是同一个。
	fallbackNote := ""
	if !ok {
		// PATH 上的 go 跑不起来或解析不出版本号（没装、或者输出格式变了）时，
		// 退回编译 gokit 这个二进制所用的工具链版本兜底。这条兜底必须留着
		// PATH 上完全没有 go 的场景不至于连个数字都报不出来，但要知道它可能
		// 与 gokit new/wire 实际会调用的那个 go 不是同一个——预编译二进制
		// 分发、机器上装了多个 Go、CI 里 gokit 来自缓存，都会让这个数字失真。
		v = runtime.Version()
		fallbackNote = "；PATH 上没有可执行的 go，这个版本号来自编译 gokit 这个二进制所用的工具链，不代表 PATH 上会被 new/wire 实际调用的那个 go"
	}
	minor, parsed := goMinor(v)
	if !parsed {
		return check{name: "Go 版本", ok: false, detail: "认不出版本号 " + v + fallbackNote}
	}
	if minor < minGoMinor {
		return check{
			name:   "Go 版本",
			ok:     false,
			detail: fmt.Sprintf("%s，低于框架要求的 1.%d%s", v, minGoMinor, fallbackNote),
		}
	}
	detail := fmt.Sprintf(
		"%s（框架要求 ≥1.%d；带 --sql 生成的项目要求 ≥1.%d，来自 SQLite 驱动，不是 gokit 的要求）%s",
		v, minGoMinor, minGoMinorSQL, fallbackNote,
	)
	return check{name: "Go 版本", ok: true, detail: detail}
}

// goVersionFromPATH 执行 PATH 上的 go version 并解析出版本号，形如 go1.27.1。
// 这是 gokit new、gokit wire 实际会调用的那个 go，不一定与编译出 gokit 这个
// 二进制的工具链是同一个。
func goVersionFromPATH() (string, bool) {
	out, err := exec.Command("go", "version").Output()
	if err != nil {
		return "", false
	}
	// 标准输出形如 "go version go1.27.1 linux/amd64\n"。
	for _, field := range strings.Fields(string(out)) {
		if strings.HasPrefix(field, "go1.") {
			return field, true
		}
	}
	return "", false
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
