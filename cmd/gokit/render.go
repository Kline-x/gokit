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
