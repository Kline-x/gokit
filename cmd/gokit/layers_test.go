package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeGo 在 root 下按相对路径写一个只有 import 的 Go 文件，包名固定是 p。
func writeGo(t *testing.T, root, rel string, imports ...string) {
	t.Helper()
	writeGoPkg(t, root, rel, "p", imports...)
}

// writeGoPkg 与 writeGo 相同，但可以指定包名——用于需要写出真实的
// package xxx_test 外部测试包这类场景，CheckLayers 本身按目录路径判定
// 模块与层，不看包名，但用例名字若声称在验某种包形态，就该真的写出那种
// 包形态，不然读代码的人会被注释带偏。
func writeGoPkg(t *testing.T, root, rel, pkg string, imports ...string) {
	t.Helper()

	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}

	var b strings.Builder
	b.WriteString("package " + pkg + "\n\nimport (\n")
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
	if len(got.Violations) != 0 {
		t.Errorf("干净的树不该有违反，实际 = %v", got.Violations)
	}
	if got.FilesChecked != 4 {
		t.Errorf("FilesChecked = %d, want 4", got.FilesChecked)
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
	if len(got.Violations) != 1 {
		t.Fatalf("违反数 = %d, want 1：%v", len(got.Violations), got.Violations)
	}
	v := got.Violations[0]
	if !strings.Contains(v.String(), "sqldb") {
		t.Errorf("违反信息里没点出是哪个 import：%s", v)
	}
	// 对字段做逐一断言：File 必须是相对 root 的斜杠路径，Layer/Import/Rule
	// 要能对得上具体是哪一条判定命中的，不能只靠数量断言蒙混过关。
	wantFile := "internal/user/domain/user.go"
	if v.File != wantFile {
		t.Errorf("File = %q, want %q", v.File, wantFile)
	}
	if v.Layer != layerDomain {
		t.Errorf("Layer = %q, want %q", v.Layer, layerDomain)
	}
	if v.Import != "github.com/Kline-x/gokit/component/sqldb" {
		t.Errorf("Import = %q, want sqldb 的完整路径", v.Import)
	}
	if v.Rule != "domain 只能依赖标准库" {
		t.Errorf("Rule = %q, want %q", v.Rule, "domain 只能依赖标准库")
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
	if len(got.Violations) != 1 {
		t.Fatalf("违反数 = %d, want 1：%v", len(got.Violations), got.Violations)
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
	if len(got.Violations) != 1 {
		t.Fatalf("违反数 = %d, want 1：%v", len(got.Violations), got.Violations)
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
	if len(got.Violations) != 1 {
		t.Fatalf("违反数 = %d, want 1：%v", len(got.Violations), got.Violations)
	}
}

func TestCheckLayersAllowsCrossModuleApplicationImport(t *testing.T) {
	root := t.TempDir()
	// 跨模块调用只能走对方的 application 接口，这一条是允许的。
	writeGo(t, root, "internal/order/application/service.go",
		"example.com/app/internal/user/application")
	// user 必须真的被识别成业务模块（有四层子目录且有文件），否则走的是
	// businessModules 里「模块不存在」那条 !inside 放行路径，测不到规则 4
	// 真正的放行分支——把这段删掉，这个测试照样绿，就是假绿。
	writeGo(t, root, "internal/user/domain/user.go", "context")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got.Violations) != 0 {
		t.Errorf("跨模块引用对方 application 是允许的，实际 = %v", got.Violations)
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
	if len(got.Violations) != 0 {
		t.Errorf("四层之外的目录不该被判违反，实际 = %v", got.Violations)
	}
	// 把 moduleAndLayer 的四层白名单整个删掉，remote/client.go 会以
	// layer="remote" 进入 judge，而 judge 的 switch 没有对应 case、直接
	// 落到末尾返回不违反——这个测试当时依然 0 违反通过，是假绿。
	// FilesChecked 断言能识破这种改法：只有 internal/user/domain/user.go
	// 落在 internal/<模块>/<层> 形状里，应该恰好是 1。
	if got.FilesChecked != 1 {
		t.Errorf("FilesChecked = %d, want 1（只有 internal/user/domain/user.go 落在四层形状里）", got.FilesChecked)
	}
}

func TestCheckLayersOnMissingDirectoryIsNotAnError(t *testing.T) {
	// 一个还没有 internal 的目录不算出错，只是没东西可查。
	got, err := CheckLayers(t.TempDir())
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got.Violations) != 0 {
		t.Errorf("空目录不该有违反，实际 = %v", got.Violations)
	}
	if got.FilesChecked != 0 {
		t.Errorf("FilesChecked = %d, want 0", got.FilesChecked)
	}
}

// --- 以下是本轮代码评审新增的用例 ---

// 模块路径不含点（如 go mod init myapp 这种完全合法的写法）时，之前
// isStdlib 会把本项目自己的 import 误判成标准库，导致 judge 连 switch 都
// 走不到，四条规则全部静默失效。这里用真实的 go.mod 搭一棵树来验证修复。
func TestCheckLayersDetectsModuleWithoutDotInPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module myapp\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatalf("写 go.mod 失败: %v", err)
	}
	writeGo(t, root, "internal/user/infrastructure/repo.go",
		"database/sql", "myapp/internal/user/interfaces")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got.Violations) != 1 {
		t.Fatalf("违反数 = %d, want 1（模块路径不含点不该让检查失效）：%v",
			len(got.Violations), got.Violations)
	}
}

// 不符合 internal/<模块>/<层> 形状的树（例如单模块布局）应当报告
// 「没有检查任何文件」，不能和「查过且干净」混在一起都返回空切片。
func TestCheckLayersReportsZeroFilesCheckedForUnrecognizedShape(t *testing.T) {
	root := t.TempDir()
	// 单模块布局：internal 下直接是层目录，缺一层模块名，相对路径只有 2 段。
	writeGo(t, root, "internal/domain/user.go", "context")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got.Violations) != 0 {
		t.Errorf("不认识的目录形状不该报违反，实际 = %v", got.Violations)
	}
	if got.FilesChecked != 0 {
		t.Errorf("FilesChecked = %d, want 0（这棵树里没有 internal/<模块>/<层> 形状的文件）",
			got.FilesChecked)
	}
}

// 规则 3 的另一半方向：infrastructure 引用本模块 interfaces。
func TestCheckLayersCatchesInfrastructureImportingInterfaces(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "internal/user/infrastructure/x.go",
		"example.com/app/internal/user/interfaces")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got.Violations) != 1 {
		t.Fatalf("违反数 = %d, want 1：%v", len(got.Violations), got.Violations)
	}
}

// 规则 4 的违反侧：interfaces 跨模块直接引用对方的 infrastructure。
func TestCheckLayersCatchesInterfacesCrossModuleInfrastructure(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "internal/order/interfaces/x.go",
		"example.com/app/internal/user/infrastructure")
	// 让 user 真的被识别成业务模块，否则模块集合逻辑会把它当成共享包放行。
	writeGo(t, root, "internal/user/domain/user.go", "context")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got.Violations) != 1 {
		t.Fatalf("违反数 = %d, want 1：%v", len(got.Violations), got.Violations)
	}
}

// 规则 4 的违反侧：application 跨模块直接引用对方的 domain。
func TestCheckLayersCatchesApplicationCrossModuleDomain(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "internal/order/application/x.go",
		"example.com/app/internal/user/domain")
	// 让 user 真的被识别成业务模块，否则模块集合逻辑会把它当成共享包放行。
	writeGo(t, root, "internal/user/domain/user.go", "context")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got.Violations) != 1 {
		t.Fatalf("违反数 = %d, want 1：%v", len(got.Violations), got.Violations)
	}
}

// application 引用第三方包的放行路径：不该被判违反。
func TestCheckLayersAllowsApplicationImportingThirdParty(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "internal/user/application/x.go",
		"github.com/Kline-x/gokit/transport")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got.Violations) != 0 {
		t.Errorf("application 引用第三方包应当放行，实际 = %v", got.Violations)
	}
}

// internal/pkg/errors 这类共享内部包，第二段不是四层之一，任何层引用它
// 都不该被误判成「引用了别的业务模块」。
//
// internal/pkg 必须真的建出来（有文件，但没有四层子目录），否则测的只是
// 「pkg 这个目录压根不存在」，守不住 businessModules 那条「有没有四层子
// 目录」的判据——把 businessModules 改成「internal 下任意目录都算模块」，
// 这个测试之前照样是绿的，就是假绿。
func TestCheckLayersAllowsSharedInternalPackage(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "internal/pkg/errors/e.go", "errors")
	writeGo(t, root, "internal/user/application/x.go",
		"example.com/app/internal/pkg/errors")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got.Violations) != 0 {
		t.Errorf("internal/pkg 这类共享包不该被当成业务模块，实际 = %v", got.Violations)
	}
}

// 测试文件引入第三方测试辅助库是正常的，domain 那条规则对 _test.go 放宽。
func TestCheckLayersAllowsDomainTestFileImportingThirdParty(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "internal/user/domain/user_test.go",
		"github.com/stretchr/testify/assert")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got.Violations) != 0 {
		t.Errorf("domain 的测试文件引入第三方测试库应当放行，实际 = %v", got.Violations)
	}
}

// testdata 下的文件即便语法不合法，也不该参与解析——否则会把「有违反」
// 变成「出错」。
func TestCheckLayersSkipsTestdata(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "internal/user/domain/user.go", "context")

	badPath := filepath.Join(root, "internal/user/domain/testdata/bad.go")
	if err := os.MkdirAll(filepath.Dir(badPath), 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	if err := os.WriteFile(badPath, []byte("这不是合法的 Go 代码 {{{"), 0o600); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v（testdata 不该被解析）", err)
	}
	if len(got.Violations) != 0 {
		t.Errorf("干净的树不该有违反，实际 = %v", got.Violations)
	}
}

// --- 以下是本轮复审新增的用例：parseInternalImport 曾经把「跨模块走侧门」
// 变成静默放行，这里补上验证 ---

// 跨模块直接引用对方的非层目录（remote 这类出站适配器）是「跨模块走侧门」，
// 规则 4 要求跨模块只能走对方的 application，这条必须判违反。
func TestCheckLayersCatchesApplicationCrossModuleSideDoor(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "internal/order/application/x.go",
		"example.com/app/internal/user/remote")
	// 让 user 真的被识别成业务模块（domain 子目录里有文件），否则新的模块
	// 集合逻辑会把它当成共享包放行，这个测试就是假绿的。
	writeGo(t, root, "internal/user/domain/user.go", "context")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got.Violations) != 1 {
		t.Fatalf("违反数 = %d, want 1（跨模块引用对方 remote 是走侧门）：%v", len(got.Violations), got.Violations)
	}
}

// 跨模块直接引用对方的模块根包（没有第三段，例如 module.go 所在的目录）
// 同样是「跨模块走侧门」，要判违反。
func TestCheckLayersCatchesInterfacesCrossModuleRootPackage(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "internal/order/interfaces/x.go",
		"example.com/app/internal/user")
	writeGo(t, root, "internal/user/domain/user.go", "context")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got.Violations) != 1 {
		t.Fatalf("违反数 = %d, want 1（跨模块引用对方模块根包是走侧门）：%v", len(got.Violations), got.Violations)
	}
}

// 同模块内引用非层目录（remote）也要落进「application 只能 import 本模块
// domain」的判定，不能因为 remote 不是四层名字就悄悄放行。
func TestCheckLayersCatchesApplicationSameModuleSideDoor(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "internal/user/application/x.go",
		"example.com/app/internal/user/remote")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got.Violations) != 1 {
		t.Fatalf("违反数 = %d, want 1（同模块引用 remote 不是 domain）：%v", len(got.Violations), got.Violations)
	}
}

// --- 以下验证 modulePathOf 对常见 go.mod 写法的容错 ---

func writeModFile(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(content), 0o600); err != nil {
		t.Fatalf("写 go.mod 失败: %v", err)
	}
}

func TestModulePathOfHandlesTrailingComment(t *testing.T) {
	root := t.TempDir()
	writeModFile(t, root, "module myapp // 注释\n\ngo 1.22\n")

	got, ok := modulePathOf(root)
	if !ok || got != "myapp" {
		t.Errorf("modulePathOf() = (%q, %v), want (\"myapp\", true)", got, ok)
	}
}

func TestModulePathOfHandlesTabSeparator(t *testing.T) {
	root := t.TempDir()
	writeModFile(t, root, "module\tmyapp\n\ngo 1.22\n")

	got, ok := modulePathOf(root)
	if !ok || got != "myapp" {
		t.Errorf("modulePathOf() = (%q, %v), want (\"myapp\", true)", got, ok)
	}
}

func TestModulePathOfHandlesQuotedName(t *testing.T) {
	root := t.TempDir()
	writeModFile(t, root, "module \"myapp\"\n\ngo 1.22\n")

	got, ok := modulePathOf(root)
	if !ok || got != "myapp" {
		t.Errorf("modulePathOf() = (%q, %v), want (\"myapp\", true)", got, ok)
	}
}

func TestModulePathOfRejectsParenBlock(t *testing.T) {
	root := t.TempDir()
	writeModFile(t, root, "module (\n\tmyapp\n)\n\ngo 1.22\n")

	_, ok := modulePathOf(root)
	if ok {
		t.Errorf("modulePathOf() ok = true, want false（括号块形式不应该被当成有效模块名）")
	}
}

// --- 以下验证同模块同层自引用被放行（子包与 x_test 两种形状） ---

// domain 拆出子包（如 domain/valueobject）是领域模型长大后的标准做法，
// 不该被当成「domain 只能依赖标准库」的违反。
func TestCheckLayersAllowsDomainSubpackage(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "internal/user/domain/user.go",
		"context", "example.com/app/internal/user/domain/valueobject")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got.Violations) != 0 {
		t.Errorf("domain 引用本层子包不该被判违反，实际 = %v", got.Violations)
	}
}

// package domain_test 引用同目录的 domain 包，是 Go 里最标准的外部测试包
// 写法，不该被当成「domain 只能依赖标准库」的违反。
//
// 用 writeGoPkg 真的写出 package domain_test（而不是 writeGo 固定的
// package p），让这个用例名副其实。不过要说明：CheckLayers 判定模块与层
// 靠的是目录路径（moduleAndLayer），完全不看文件内声明的包名，所以包名
// 是不是 domain_test 并不影响 judge() 的走向——这里真正被验证的，是「同
// 模块同层自引用」这条放行分支（user_test.go 与 user.go 同属 internal/
// user/domain 目录），包名只是让测试代码本身更贴近真实的外部测试包写法，
// 不产生新的断言点。
func TestCheckLayersAllowsDomainExternalTestPackage(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "internal/user/domain/user.go", "context")
	writeGoPkg(t, root, "internal/user/domain/user_test.go", "domain_test",
		"example.com/app/internal/user/domain")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got.Violations) != 0 {
		t.Errorf("domain_test 引用同目录 domain 不该被判违反，实际 = %v", got.Violations)
	}
}

// application 拆出子包（如 application/dto）同理不该被判违反。
func TestCheckLayersAllowsApplicationSubpackage(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "internal/user/application/s.go",
		"example.com/app/internal/user/application/dto")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got.Violations) != 0 {
		t.Errorf("application 引用本层子包不该被判违反，实际 = %v", got.Violations)
	}
}

// 上面两个子包用例（valueobject、dto）只搭了引用方，没有真的把被引用的
// 子包目录建出来——它们只证明「引用子包不算违反」，没有顺带证明「子包
// 目录里的文件本身仍然受所在层的规则约束」。moduleAndLayer 只看
// internal/<模块>/<层>/... 里的第二段，子包只是让路径多一段，判定的
// module、layer 不会变，所以规则理应对子包里的文件同样生效——这里补一个
// 正向用例钉死这一点：domain 拆出来的 valueobject 子包里的文件本身，
// 一样不能越过 domain 只能依赖标准库这条规则。
func TestCheckLayersEnforcesRulesInsideSubpackageItself(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "internal/user/domain/valueobject/v.go",
		"github.com/stretchr/testify/assert")

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v", err)
	}
	if len(got.Violations) != 1 {
		t.Fatalf("违反数 = %d, want 1（子包里的文件本身仍然受 domain 只能依赖标准库约束）：%v",
			len(got.Violations), got.Violations)
	}
}

// --- 以下验证单个文件解析失败不会中断整棵树的检查 ---

// 单个文件解析失败不该让整棵树的检查中断——已经发现的违反照常报出来，
// 解析失败的文件单独记录在 ParseFailures 里。
func TestCheckLayersContinuesAfterParseFailure(t *testing.T) {
	root := t.TempDir()
	// 一个违反：domain 引用了框架包。
	writeGo(t, root, "internal/user/domain/user.go",
		"context", "github.com/Kline-x/gokit/component/sqldb")
	// 另一个模块下，一个语法不合法的 .go 文件（不在 testdata 里，真实场景
	// 里可能是生成中或模板残留的文件）。
	badPath := filepath.Join(root, "internal/order/application/broken.go")
	if err := os.MkdirAll(filepath.Dir(badPath), 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	if err := os.WriteFile(badPath, []byte("这不是合法的 Go 代码 {{{"), 0o600); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}

	got, err := CheckLayers(root)
	if err != nil {
		t.Fatalf("CheckLayers() error = %v（单个文件解析失败不该让整棵树的检查中断）", err)
	}
	if len(got.Violations) != 1 {
		t.Fatalf("违反数 = %d, want 1（已发现的违反不该因为别处解析失败而丢失）：%v", len(got.Violations), got.Violations)
	}
	if len(got.ParseFailures) != 1 {
		t.Fatalf("ParseFailures 数 = %d, want 1：%v", len(got.ParseFailures), got.ParseFailures)
	}
	wantFile := "internal/order/application/broken.go"
	if got.ParseFailures[0].File != wantFile {
		t.Errorf("ParseFailures[0].File = %q, want %q", got.ParseFailures[0].File, wantFile)
	}
}
