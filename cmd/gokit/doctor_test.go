package main

import (
	"bytes"
	"os"
	"path/filepath"
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

// 没有 internal 目录的树上跑 doctor，输出必须说明「未检查任何文件」，不能
// 和「查过且干净」共用同一句「没有发现违反」——那样使用者看到的绿灯可能
// 只是因为目录形状没对上，根本没检查任何文件。
func TestDoctorReportsZeroFilesChecked(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	code := runDoctor(&buf, []string{dir})

	out := buf.String()
	if !strings.Contains(out, "未检查任何文件") {
		t.Errorf("没有 internal 目录时，输出里应该说明未检查任何文件：\n%s", out)
	}
	if code != 0 {
		t.Errorf("退出码 = %d, want 0（没检查任何文件不算失败）", code)
	}
}

// --require-files 打开时，一个文件都没检查到应该返回非零退出码——这是
// 给 CI 接的回归护栏用的开关：CI 今天跑 doctor 的那个目录恰好有文件，
// 哪天目录形状变了，这条护栏不该从「查过且干净」静默退化成「什么都
// 没查」还照样绿灯。
func TestDoctorRequireFilesFlagFailsWhenNoFilesChecked(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	code := runDoctor(&buf, []string{"--require-files", dir})

	if code == 0 {
		t.Errorf("--require-files 打开、且一个文件都没检查到时，退出码不该是 0")
	}
	if !strings.Contains(buf.String(), "未检查任何文件") {
		t.Errorf("输出里应该说明未检查任何文件：\n%s", buf.String())
	}
}

// --require-files 打开时，只要真的检查到了文件（哪怕干净、没有违反），
// 依旧应该正常通过——这个开关只管「有没有查过」，不改变「查完发现什么」
// 的判定。
func TestDoctorRequireFilesFlagPassesWhenFilesChecked(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "internal/user/domain/user.go", "context")

	var buf bytes.Buffer
	code := runDoctor(&buf, []string{"--require-files", root})

	if code != 0 {
		t.Errorf("--require-files 打开、但确实检查到了文件时，退出码不该非零：\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "检查了") {
		t.Errorf("输出里应该报出检查了多少个文件：\n%s", buf.String())
	}
}

// 有文件解析失败时，doctor 要照实报出来，且退出码不能是 0。
func TestDoctorReportsParseFailuresAndFailsNonZero(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "internal/order/application/broken.go")
	if err := os.MkdirAll(filepath.Dir(badPath), 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	if err := os.WriteFile(badPath, []byte("这不是合法的 Go 代码 {{{"), 0o600); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}

	var buf bytes.Buffer
	code := runDoctor(&buf, []string{dir})

	if code == 0 {
		t.Errorf("有解析失败的文件时退出码不该是 0")
	}
	if !strings.Contains(buf.String(), "解析失败") {
		t.Errorf("输出里应该报出解析失败的文件：\n%s", buf.String())
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

// toolchainChecks 必须显式检查 go 本身在不在 PATH 上：gokit new 要跑
// go mod tidy、gokit wire 要跑 go build，没有 go 这两条命令都走不了，
// 之前 doctor 却对此只字不提。
func TestToolchainChecksIncludesGoBinary(t *testing.T) {
	requireTool(t, "go")

	for _, c := range toolchainChecks() {
		if c.name == "go" {
			if !c.ok {
				t.Errorf("PATH 上明明有 go，go 这一项检查却判定未通过：%s", c.detail)
			}
			return
		}
	}
	t.Fatal("toolchainChecks 里没有单独检查 go 二进制是否在 PATH 上")
}

// goVersionFromPATH 必须真的去执行 PATH 上的 go version，而不是用编译出
// gokit 这个测试二进制的工具链版本代替——预编译分发、机器上装了多个 Go
// 时，两者可能不是同一个版本。
func TestGoVersionFromPATHParsesRealBinary(t *testing.T) {
	requireTool(t, "go")

	v, ok := goVersionFromPATH()
	if !ok {
		t.Fatalf("goVersionFromPATH() 应该能解析出 PATH 上 go 的版本")
	}
	if _, parsed := goMinor(v); !parsed {
		t.Errorf("goVersionFromPATH() 返回的 %q 不是 goMinor 能解析的形状", v)
	}
}

// doctor 的输出要把「框架要求 1.22」与「--sql 生成的项目要求 1.25（来自
// SQLite 驱动）」分开说清楚，不能只报一个笼统的下限，让人误以为符合框架
// 下限就够用了——带 --sql 的默认参数生成的 go.mod 实际写的是 1.25。
func TestGoVersionCheckDetailMentionsBothMinimums(t *testing.T) {
	c := goVersionCheck()
	if !c.ok {
		t.Skip("本机 Go 版本检查未通过，跳过措辞断言")
	}
	if !strings.Contains(c.detail, "1.22") {
		t.Errorf("Go 版本检查的说明里应该点出框架下限 1.22：%s", c.detail)
	}
	if !strings.Contains(c.detail, "1.25") {
		t.Errorf("Go 版本检查的说明里应该点出 --sql 生成项目的下限 1.25：%s", c.detail)
	}
}
