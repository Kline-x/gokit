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
	// ParseFailures 是解析失败的文件，单个文件解析失败不该让整棵树的检查
	// 中断——已经发现的违反要照常报出来，解析失败的文件另外单独列出。
	ParseFailures []ParseFailure
}

// ParseFailure 记录一个解析失败的文件。
type ParseFailure struct {
	// File 是解析失败的文件，相对于被检查的根目录。
	File string
	// Err 是解析时报的错误。
	Err error
}

func (p ParseFailure) String() string {
	return fmt.Sprintf("%s 解析失败: %v", p.File, p.Err)
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

	// 先扫一遍 internal 下有哪些目录称得上「业务模块」——含有至少一个四层
	// 子目录、且子目录里确实有文件的，才算数。internal/pkg、internal/parity
	// 这类共享包不在其列，它们不受四层规则约束。这个集合同时喂给下面的
	// judge/parseInternalImport，用来区分「跨模块走侧门」（模块在集合里，
	// 但引用的不是对方 application）和「引用共享包」（模块根本不在集合里）
	// 这两种截然不同的情况。
	modules, err := businessModules(internal)
	if err != nil {
		return LayersResult{}, fmt.Errorf("gokit: 扫描 %s 下的业务模块失败: %w", internal, err)
	}

	var violations []violation
	var parseFailures []ParseFailure
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

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		imports, parseErr := importsOf(path)
		if parseErr != nil {
			// 单个文件解析失败（生成中的、模板残留的语法不合法的 .go）不该
			// 让整棵树的检查中断——记下来继续走下一个文件，已经发现的违反
			// 照常保留。
			parseFailures = append(parseFailures, ParseFailure{File: rel, Err: parseErr})
			return nil
		}
		filesChecked++

		isTest := strings.HasSuffix(d.Name(), "_test.go")

		for _, imp := range imports {
			if rule, bad := judge(layer, module, imp, isStdlib, isTest, modules); bad {
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

	return LayersResult{Violations: violations, FilesChecked: filesChecked, ParseFailures: parseFailures}, nil
}

// businessModules 找出 internal 下真正的「业务模块」目录：至少有一个四层
// （domain/application/infrastructure/interfaces）子目录、且那个子目录里
// 确实有文件的，才算数。
//
// 这是为了把「共享内部包」（如 internal/pkg、internal/parity，没有任何四层
// 子目录）和「业务模块」（如 internal/user，有 domain/application/... 这些
// 子目录）区分开：前者不受四层依赖方向规则约束，后者受约束，即便代码绕开
// 四层、直接引用对方模块下的非层目录（如 internal/user/remote），也该算
// 「跨模块走侧门」，判违反。
func businessModules(internal string) (map[string]bool, error) {
	entries, err := os.ReadDir(internal)
	if err != nil {
		return nil, err
	}

	modules := make(map[string]bool)
	for _, e := range entries {
		if !e.IsDir() || skipDir(e.Name()) {
			continue
		}
		has, err := moduleHasLayerFiles(filepath.Join(internal, e.Name()))
		if err != nil {
			return nil, err
		}
		if has {
			modules[e.Name()] = true
		}
	}
	return modules, nil
}

// moduleHasLayerFiles 判断某个 internal 下的目录，是不是至少有一个四层
// 子目录，且那个子目录里确实有文件。
func moduleHasLayerFiles(moduleDir string) (bool, error) {
	for _, layer := range [...]string{layerDomain, layerApplication, layerInfrastructure, layerInterfaces} {
		has, err := dirHasAnyFile(filepath.Join(moduleDir, layer))
		if err != nil {
			return false, err
		}
		if has {
			return true, nil
		}
	}
	return false, nil
}

// dirHasAnyFile 判断一个目录（递归地，跳过 testdata/vendor 等）下是否
// 至少有一个文件。目录不存在时视为没有，不算错误。
func dirHasAnyFile(dir string) (bool, error) {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !info.IsDir() {
		return false, nil
	}

	found := false
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		found = true
		return filepath.SkipAll
	})
	if err != nil {
		return false, err
	}
	return found, nil
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
//
// 只按行扫是简化实现，不是完整的 go.mod 语法解析：真解析要么手写状态机
// 覆盖注释/续行等边界情况，要么引入 golang.org/x/mod/modfile 这类第三方
// 库，两者都超出这里的成本预算。为了不让常见写法（行尾注释、tab 分隔、
// module 用括号块）静默把自家包误判成标准库，这里额外处理：
//   - module 后面必须紧跟空白（空格或 tab），排除 "modulefoo" 这类误命中；
//   - 只取第一个空白分隔的字段，行尾注释（`module myapp // 注释`）不会被
//     带进模块名；
//   - 去掉模块名两侧可能的引号（go.mod 允许 `module "myapp"`）；
//   - 遇到 `module (` 这种块形式，直接判 ok=false，不把 "(" 当模块名用。
func modulePathOf(root string) (path string, ok bool) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		rest, cut := strings.CutPrefix(trimmed, "module")
		if !cut || rest == "" {
			continue
		}
		if rest[0] != ' ' && rest[0] != '\t' {
			// "module" 后面没跟空白，不是真正的 module 指令（例如某个碰巧
			// 以 module 开头的标识符）。
			continue
		}
		rest = strings.TrimSpace(rest)
		if rest == "" {
			continue
		}
		if strings.HasPrefix(rest, "(") {
			// module (\n ... \n) 块形式：第一段是 "("，不是有效模块名。
			return "", false
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		name := strings.Trim(fields[0], `"`)
		if name == "" {
			continue
		}
		return name, true
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
//
// 解析失败时返回的 error 不带文件路径——调用方（CheckLayers）已经知道是
// 哪个文件，会在 ParseFailure 里配上相对路径，这里重复一遍只会让报告
// 里同一个路径出现两次。
func importsOf(path string) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
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
//
// modules 是 businessModules 扫出来的业务模块集合，传给 parseInternalImport
// 用来区分「引用共享内部包」（模块不在集合里，放行）和「跨模块走侧门」
// （模块在集合里，但引用的不是对方的四层目录本身或不是走 application）。
func judge(layer, module, imp string, isStdlib func(string) bool, isTest bool, modules map[string]bool) (rule string, bad bool) {
	if isStdlib(imp) {
		return "", false
	}

	otherModule, otherLayer, inside := parseInternalImport(imp, modules)

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

// parseInternalImport 认出一条 import 指向 internal 下的哪个模块（以及
// 如果有第三段的话，哪一层）。
//
// 认的是路径里出现 internal/<模块>/... 的形状，不关心前面的模块路径是
// 什么，这样检查就不必知道项目自己的 module path。取的是最后一个 internal
// 段，避免模块路径自身含 internal 段时（例如 github.com/acme/internal/app）
// 错位取到前面那截。
//
// 这里回答的问题是「这条 import 指向谁」，和 moduleAndLayer 回答的「这个
// 文件受不受四层约束」是两个不同的问题——不能强行对齐成同一个判据：
//
//   - internal/pkg/errors 该放行，是因为 pkg 不是业务模块（modules 集合里
//     没有它），不管它的第二段长得像不像层名。
//   - internal/user/remote 不该放行，是因为 user 是业务模块：跨模块引用
//     它任何一个非 application 的子目录（甚至模块根包本身），都是「跨模块
//     走侧门」，要照常判违反；同模块内引用 remote 这类非层目录，也要落进
//     「只能 import 本模块 domain」的判定，不能因为 remote 不是四层名字
//     就悄悄放行。
//
// 所以判断放不放行的分界点是模块名在不在 modules 集合里，不是第三段像不
// 像层名：
//
//   - 模块位段在 modules 里：ok=true，第三段原样返回（不是四层名时返回
//     那个原始段；没有第三段——即 internal/<模块> 本身或 module.go 这类
//     装配文件所在的目录——时返回空串）。调用方 judge 里同模块/跨模块的
//     分支会按现有逻辑，把非 domain/非 application 的这些值当违反处理。
//   - 模块位段不在 modules 里（internal/pkg、internal/parity 这类共享
//     包）：ok=false，视同外部包放行。
func parseInternalImport(imp string, modules map[string]bool) (module, layer string, ok bool) {
	parts := strings.Split(imp, "/")

	idx := -1
	for i, p := range parts {
		if p == "internal" {
			idx = i
		}
	}
	if idx == -1 || idx+1 >= len(parts) {
		return "", "", false
	}

	module = parts[idx+1]
	if !modules[module] {
		return "", "", false
	}

	if idx+2 < len(parts) {
		layer = parts[idx+2]
	}
	return module, layer, true
}
