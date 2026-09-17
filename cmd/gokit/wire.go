package main

import (
	"flag"
	"fmt"
	"io"
)

// runWire 重新生成装配代码，然后构建一次。
//
// 多跑的那次构建不是多余的：wire 生成成功不等于结果能编译——
// 比如提供者签名变了但调用方没跟上。不当场发现，就要等到下次运行才发现。
func runWire(w io.Writer, args []string) int {
	fs := flag.NewFlagSet("wire", flag.ContinueOnError)
	fs.SetOutput(w)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}

	if err := runIn(dir, "wire", "./..."); err != nil {
		fmt.Fprintf(w, "gokit: 生成装配代码失败：%v\n", err)
		return 1
	}
	fmt.Fprintln(w, "装配代码已重新生成")

	if err := runIn(dir, "go", "build", "./..."); err != nil {
		fmt.Fprintf(w, "gokit: 生成后构建不通过：%v\n", err)
		return 1
	}
	fmt.Fprintln(w, "构建通过")
	return 0
}
