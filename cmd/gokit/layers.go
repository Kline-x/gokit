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
