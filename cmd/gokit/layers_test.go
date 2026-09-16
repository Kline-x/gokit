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
