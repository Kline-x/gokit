package main

import (
	"errors"
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
//
// 发新版且模板用到了新 API 时，这里要跟着 bump，否则生成的项目 go.mod
// 里锁的还是旧版本，可能缺新模板依赖的包或符号。
const defaultGokitVersion = "v0.1.0"

func runNew(w io.Writer, args []string) int {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	fs.SetOutput(w)

	// flag.Parse 遇到第一个不以 "-" 开头的参数就会停止解析，把它和它后面的
	// 一切都当成位置参数。用法提示写的是「目录在前，flags 在后」（更符合
	// 直觉的顺序），所以这里把开头那个位置参数挪到末尾，flag.Parse 扫完
	// 所有 flags 之后自然会在它这里停下，不影响「flags 在前」的写法。
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		rotated := make([]string, 0, len(args))
		rotated = append(rotated, args[1:]...)
		rotated = append(rotated, args[0])
		args = rotated
	}

	modPath := fs.String("mod", "", "go.mod 的模块路径，必填，例如 example.com/myapp")
	module := fs.String("module", "hello", "生成的示例业务模块名")
	version := fs.String("gokit-version", defaultGokitVersion, "依赖的 gokit 版本")
	replace := fs.String("replace", "", "指向本地 gokit 检出的路径，框架开发时用")
	withSQL := fs.Bool("sql", true, "生成数据库组件与仓储实现")
	withGRPC := fs.Bool("grpc", false, "生成 gRPC 服务端与 proto")
	skipTools := fs.Bool("skip-tools", false, "只落盘，不跑 go mod tidy 与 wire")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			// -h/--help 是使用者主动要求看帮助，不是用错了参数，退出码该是
			// 0——脚本、CI 里 `gokit new -h` 不该被当成执行失败。
			return 0
		}
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
	if strings.ContainsAny(*modPath, " \t\r\n") {
		fmt.Fprintf(w, "gokit: --mod 的值 %q 不能包含空白字符\n", *modPath)
		return 2
	}
	if !validGoIdentifier(*module) {
		fmt.Fprintf(w, "gokit: --module 的值 %q 不是合法的 Go 标识符（须以字母开头，"+
			"其余字符只能是字母、数字或下划线，且不能是 Go 保留字），"+
			"它会被原样用作生成代码里的包名\n", *module)
		return 2
	}
	if err := checkEmpty(dst); err != nil {
		fmt.Fprintf(w, "gokit: %v\n", err)
		return 1
	}

	name := lastSegment(*modPath)
	data := projectData{
		ModPath:      *modPath,
		Name:         name,
		Module:       *module,
		ModuleTitle:  title(*module),
		EnvPrefix:    envPrefix(name),
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
		if *withGRPC {
			fmt.Fprintln(w, "跳过了 go mod tidy、proto 生成与 wire，记得自己跑一遍 make proto 和 gokit wire（或 make wire）")
		} else {
			fmt.Fprintln(w, "跳过了 go mod tidy 与 wire，记得自己跑一遍 gokit wire（或 make wire）")
		}
		return 0
	}

	// 顺序不能反：interfaces/grpc.go import 的 api/.../v1 包要先有 .pb.go 才存在，
	// 无论是 go mod tidy 还是 wire 接下来解析依赖都要用到它。
	if *withGRPC {
		if err := runProtoc(dst); err != nil {
			// 这里 return 的时候项目里既没有 go.sum 也没有 wire_gen.go：
			// 光说「跑 make proto 就行」不够，使用者装完 protoc、跑完
			// make proto 之后，还会依次卡在缺 go.sum、undefined: initApp
			// 上，一路排查过去才知道其实还差 go mod tidy 和 gokit wire
			// 两步。把完整补救顺序一次性列全，不要让人自己踩出来。
			fmt.Fprintf(w, "gokit: proto 生成没跑成：%v\n", err)
			fmt.Fprintf(w, "文件已经生成好了，但还差几步才能构建，都在 %s 里依次执行：\n", dst)
			fmt.Fprintln(w, "  1. 装好 protoc：https://github.com/protocolbuffers/protobuf/releases")
			fmt.Fprintln(w, "  2. make tools   # 装两个 protoc 插件，版本已经钉在 Makefile 里")
			fmt.Fprintln(w, "  3. make proto   # 生成 .pb.go")
			fmt.Fprintln(w, "  4. go mod tidy  # 补齐 go.sum")
			fmt.Fprintln(w, "  5. gokit wire   # 生成装配代码 wire_gen.go（或 make wire）")
			return 1
		}
	}

	// 顺序不能反：wire 要能解析依赖才能生成，所以先 tidy。
	if err := runIn(dst, "go", "mod", "tidy"); err != nil {
		fmt.Fprintf(w, "gokit: %v\n", err)
		return 1
	}
	if err := runIn(dst, "wire", "./..."); err != nil {
		// wire 装不上不该让生成前功尽弃：文件都在，补跑一次就行。
		fmt.Fprintf(w, "gokit: 代码生成没跑成：%v\n", err)
		fmt.Fprintf(w, "文件已经生成好了，装上 wire 之后在 %s 里跑 gokit wire（或 make wire）即可。\n", dst)
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

// runProtoc 根据 dst/api 下的 .proto 生成 .pb.go，参数照抄仓库根 Makefile 的
// proto 目标：--proto_path 与两个 --xxx_out 都指向同一个目录，生成物与 .proto
// 同目录。dst 下没有任何 .proto 时什么也不做。
func runProtoc(dst string) error {
	protoDir := filepath.Join(dst, "api")
	var protoFiles []string
	err := filepath.WalkDir(protoDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && path == protoDir {
				return nil
			}
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".proto") {
			rel, relErr := filepath.Rel(dst, path)
			if relErr != nil {
				return relErr
			}
			protoFiles = append(protoFiles, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("扫描 %s 下的 proto 文件失败: %w", protoDir, err)
	}
	if len(protoFiles) == 0 {
		return nil
	}

	args := []string{
		"--proto_path=api",
		"--go_out=api", "--go_opt=paths=source_relative",
		"--go-grpc_out=api", "--go-grpc_opt=paths=source_relative",
	}
	args = append(args, protoFiles...)
	return runIn(dst, "protoc", args...)
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

// lastSegment 取模块路径的最后一段，作为生成项目的应用名。
//
// Go 模块的主版本后缀（v2、v10 这类 /vN）不算应用名的一部分：
// --mod github.com/org/myapp/v2 的末段是 "v2"，直接取最后一段会把版本号
// 当成应用名，进而污染 DSN 文件名、.gitignore 条目等一大片生成内容。
// 跳过版本后缀段，往前找第一个不是版本后缀的段。
func lastSegment(modPath string) string {
	segs := strings.Split(modPath, "/")
	for i := len(segs) - 1; i >= 0; i-- {
		if isMajorVersionSuffix(segs[i]) {
			continue
		}
		return segs[i]
	}
	return modPath
}

// isMajorVersionSuffix 判断 s 是不是形如 v2、v10 的 Go 模块主版本后缀段。
func isMajorVersionSuffix(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// envPrefix 把应用名转换成合法的环境变量名前缀。
//
// POSIX shell 的变量名只能由字母、数字、下划线组成，且不能以数字开头。
// 应用名原样大写后直接当前缀，遇到连字符（仓库名里极其常见，例如
// my-app）会产出 MY-APP_HTTP_ADDR 这种在任何 POSIX shell 里都设不了的
// 变量名——export MY-APP_HTTP_ADDR=x 本身就是语法错误。
func envPrefix(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		switch {
		case r == '_' || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" || (out[0] >= '0' && out[0] <= '9') {
		out = "_" + out
	}
	return out
}

// goKeywords 是 Go 的 25 个保留字。语法上它们和普通标识符长得一样，
// 但用作包名（--module select）编译不过：package select 是语法错误。
var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
}

// validGoIdentifier 检查 s 是否是一个合法、能安全用在生成代码里的 Go
// 标识符：首字符必须是字母，其余字符只能是字母、数字或下划线，且不能是
// Go 保留字。--module 的值会被原样用作生成代码里的包名，还会经 title()
// 大写首字母拼进类型名（{{.ModuleTitle}}Request）。
//
// 首字符不再放开下划线：下划线开头本身是合法的 Go 标识符，但 --module _
// 生成的是 package _（Go 语法不允许把 _ 当包名），--module _foo 会让
// title() 产出的类型名 _fooRequest 保持小写开头、未导出，一旦别的生成
// 文件跨包引用它就编译不过——这两种都是「语法上是标识符，用在这里就炸」，
// 不该放行。
func validGoIdentifier(s string) bool {
	if s == "" || goKeywords[s] {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
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
