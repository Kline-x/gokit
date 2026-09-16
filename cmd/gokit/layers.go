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

// LayersResult 是一次分层检查的结果。
//
// 把「检查了多少个文件」和「有多少条违反」分开报告，是为了让调用方能区分
// 「没查」和「查过且干净」——空的 Violations 配上 0 的 FilesChecked，意味着
// 这棵树里根本没有识别出符合 internal/<模块>/<层> 形状的文件，不该被当成
// 「通过」来渲染。
type LayersResult struct {
	// Violations 是发现的违反，可能为空。
	Violations []violation
	// FilesChecked 是被识别为四层之一、实际参与了判定的文件数。
	FilesChecked int
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
// 判断「是不是标准库」优先靠 root/go.mod 里的 module path：凡是以它为前缀的
// import 都是本项目自己的包，不能被当成标准库放行。读不到 go.mod 时（例如
// 测试用临时目录搭的假树），退回旧有的「路径第一段有没有点」这条惯例判断——
// 这只是退化路径，模块名不含点（如 `go mod init myapp`）时会把自家包误判成
// 标准库，应尽量避免依赖它。
//
// root/internal 不存在时不算出错，只是没东西可查——刚起步的项目就是这样。
func CheckLayers(root string) (LayersResult, error) {
	internal := filepath.Join(root, "internal")
	info, err := os.Stat(internal)
	if err != nil {
		if os.IsNotExist(err) {
			return LayersResult{}, nil
		}
		return LayersResult{}, fmt.Errorf("gokit: 检查 %s 失败: %w", internal, err)
	}
	if !info.IsDir() {
		return LayersResult{}, nil
	}

	modulePath, haveModule := modulePathOf(root)
	isStdlib := stdlibChecker(modulePath, haveModule)

	var violations []violation
	var filesChecked int

	walkErr := filepath.WalkDir(internal, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// 测试文件、testdata、vendor 与隐藏目录不是产品代码，不该被这套
			// 检查解析——testdata 里故意写坏的 Go 文件会让解析报错，把「有
			// 违反」变成「出错」。跳过规则与 Go 工具链自身一致。
			if path != internal && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}

		module, layer, ok := moduleAndLayer(internal, path)
		if !ok {
			return nil
		}
		filesChecked++

		imports, parseErr := importsOf(path)
		if parseErr != nil {
			return parseErr
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		isTest := strings.HasSuffix(d.Name(), "_test.go")

		for _, imp := range imports {
			if rule, bad := judge(layer, module, imp, isStdlib, isTest); bad {
				violations = append(violations, violation{
					File: rel, Layer: layer, Import: imp, Rule: rule,
				})
			}
		}
		return nil
	})
	if walkErr != nil {
		return LayersResult{}, fmt.Errorf("gokit: 遍历 %s 失败: %w", internal, walkErr)
	}

	return LayersResult{Violations: violations, FilesChecked: filesChecked}, nil
}

// skipDir 判断遍历时要不要整个跳过这个目录。
func skipDir(name string) bool {
	if name == "testdata" || name == "vendor" {
		return true
	}
	// Go 工具链本身也不把 "_" 或 "." 开头的目录当产品代码。
	return strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".")
}

// modulePathOf 读取 root/go.mod 里的 module 路径。
//
// 只按行扫 "module " 前缀，不引入第三方的 go.mod 解析库——这个二进制和框架
// 库同属一个模块，任何新依赖都会落进使用者的 go.mod。
//
// 读不到（不存在、无权限等任何原因）时返回 ok=false，调用方退回旧的惯例
// 判断。这条退化路径必须保留：现有测试在没有 go.mod 的临时目录里用
// example.com/app/... 这种假路径搭树，靠的就是它。
func modulePathOf(root string) (path string, ok bool) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest, cut := strings.CutPrefix(strings.TrimSpace(line), "module ")
		if !cut {
			continue
		}
		rest = strings.TrimSpace(rest)
		if rest == "" {
			continue
		}
		return rest, true
	}
	return "", false
}

// stdlibChecker 生成一个「这条 import 要不要当作标准库/不受约束的依赖放行」
// 的判断函数，绑定好当前项目的 module path。
func stdlibChecker(modulePath string, haveModule bool) func(imp string) bool {
	return func(imp string) bool {
		if haveModule && (imp == modulePath || strings.HasPrefix(imp, modulePath+"/")) {
			// 是本项目自己的包，一定不是标准库，必须继续走分层判定，
			// 不能因为模块名（如 myapp）不含点就被误判放行。
			return false
		}
		// 退化判断：标准库的导入路径第一段没有域名，第三方的有。
		first, _, _ := strings.Cut(imp, "/")
		return !strings.Contains(first, ".")
	}
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
//
// isTest 表示当前文件是不是 _test.go：测试文件引入第三方测试辅助库是正常
// 的，domain 那条「只能依赖标准库」的规则对测试文件放宽到「不能 import
// 兄弟模块」，其余规则不受影响。
func judge(layer, module, imp string, isStdlib func(string) bool, isTest bool) (rule string, bad bool) {
	if isStdlib(imp) {
		return "", false
	}

	otherModule, otherLayer, inside := parseInternalImport(imp)

	switch layer {
	case layerDomain:
		if isTest && !inside {
			// 测试文件引入的第三方测试辅助库，不受「只能依赖标准库」约束。
			return "", false
		}
		// 规则 1：domain 只能依赖标准库，也不能 import 兄弟模块（inside 为
		// true 但 otherModule != module 的情形，同样落在这条判定里）。
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

// parseInternalImport 认出一条指向某个业务模块某一层的 import。
//
// 认的是路径里出现 internal/<模块>/<层> 的形状，不关心前面的模块路径是
// 什么，这样检查就不必知道项目自己的 module path。取的是最后一个 internal
// 段，避免模块路径自身含 internal 段时（例如 github.com/acme/internal/app）
// 错位取到前面那截。
//
// <层> 必须是四层之一才算数：internal/pkg/errors 这类共享内部包的第二段
// 不是层名，这里要和 moduleAndLayer 的 default 分支保持同一个判断口径，
// 一律 ok=false 视同外部包放行，不能在这里判进某个业务模块，害它在
// moduleAndLayer 里明明不受约束、却在 parseInternalImport 里被当成别的
// 业务模块而报出跨模块违反。
//
// internal/<模块> 后面没有第三段（没有层）时，同样按 moduleAndLayer 的口径
// 处理——那正是 module.go 这类装配文件所在的目录，不受四层约束，返回
// ok=false。
func parseInternalImport(imp string) (module, layer string, ok bool) {
	parts := strings.Split(imp, "/")

	idx := -1
	for i, p := range parts {
		if p == "internal" {
			idx = i
		}
	}
	if idx == -1 || idx+2 >= len(parts) {
		return "", "", false
	}

	module, layer = parts[idx+1], parts[idx+2]
	switch layer {
	case layerDomain, layerApplication, layerInfrastructure, layerInterfaces:
		return module, layer, true
	default:
		return "", "", false
	}
}
