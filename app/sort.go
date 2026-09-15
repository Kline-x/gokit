package app

import "fmt"

// sortComponents 按 Dependent 声明的依赖做拓扑排序。
//
// 排序是确定性的：每一轮都从头扫描，挑出第一个依赖已全部就绪的组件，
// 因此互不依赖的组件保持注册顺序。
func sortComponents(cs []Component) ([]Component, error) {
	index := make(map[string]int, len(cs))
	for i, c := range cs {
		if _, dup := index[c.Name()]; dup {
			return nil, fmt.Errorf("组件名重复: %q", c.Name())
		}
		index[c.Name()] = i
	}

	indegree := make([]int, len(cs))
	dependents := make([][]int, len(cs)) // dependents[j] 记录依赖 j 的组件下标
	for i, c := range cs {
		d, ok := c.(Dependent)
		if !ok {
			continue
		}
		for _, dep := range d.DependsOn() {
			j, known := index[dep]
			if !known {
				return nil, fmt.Errorf("组件 %q 依赖了未注册的组件 %q", c.Name(), dep)
			}
			if j == i {
				return nil, fmt.Errorf("组件 %q 依赖了自己", c.Name())
			}
			indegree[i]++
			dependents[j] = append(dependents[j], i)
		}
	}

	out := make([]Component, 0, len(cs))
	done := make([]bool, len(cs))
	for len(out) < len(cs) {
		picked := -1
		for i := range cs {
			if !done[i] && indegree[i] == 0 {
				picked = i
				break
			}
		}
		if picked < 0 {
			return nil, fmt.Errorf("组件依赖存在循环，无法确定启动顺序")
		}
		done[picked] = true
		out = append(out, cs[picked])
		for _, k := range dependents[picked] {
			indegree[k]--
		}
	}
	return out, nil
}
