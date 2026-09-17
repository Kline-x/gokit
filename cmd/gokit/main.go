// Command gokit 是 gokit 框架的脚手架。
//
// 它只用标准库：这个二进制与框架库同属一个模块，引入任何第三方依赖
// 都会落进使用者的 go.mod，而使用者要的是框架，不是脚手架的依赖树。
package main

import (
	"fmt"
	"io"
	"os"
)

const usage = `gokit 是 gokit 框架的脚手架。

用法：
  gokit new <项目目录> --mod <模块路径> [--sql] [--grpc] [--replace <本地框架路径>]
        生成一个能立刻构建的新项目。

  gokit wire [目录]
        在项目里跑一遍代码生成，然后构建一次确认结果可用。默认当前目录。

  gokit doctor [目录]
        检查工具链是否齐备，以及分层依赖方向有没有被破坏。默认当前目录。

每条命令加 -h 看它自己的参数。
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "new":
		os.Exit(runNew(os.Stdout, os.Args[2:]))
	case "wire":
		os.Exit(runWire(os.Stdout, os.Args[2:]))
	case "doctor":
		os.Exit(runDoctor(os.Stdout, os.Args[2:]))
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usage)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "未知命令 %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

// runWire 由 Task 5 实现。
func runWire(w io.Writer, args []string) int {
	fmt.Fprintln(w, "gokit wire 尚未实现")
	return 2
}
