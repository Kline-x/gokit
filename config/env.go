package config

import (
	"fmt"
	"os"
	"reflect"
	"strings"
)

// Paths 按声明顺序枚举结构体的全部叶子字段路径。
// 嵌套结构体会被递归展开；time.Duration 视为叶子。
func Paths(dst any) []string {
	v := reflect.ValueOf(dst)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	var out []string
	collectPaths(v, "", &out)
	return out
}

func collectPaths(v reflect.Value, prefix string, out *[]string) {
	if v.Kind() != reflect.Struct {
		return
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.PkgPath != "" {
			continue
		}
		fv := v.Field(i)

		if isInline(sf) {
			// 内联字段的键在父级展开，沿用父前缀，字段本身不产生独立路径。
			if fv.Kind() == reflect.Struct {
				collectPaths(fv, prefix, out)
			}
			continue
		}

		name := configName(sf)
		if name == "-" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		if fv.Kind() == reflect.Struct && fv.Type() != durationType {
			collectPaths(fv, path, out)
			continue
		}
		*out = append(*out, path)
	}
}

// EnvName 把配置路径映射成环境变量名，
// 例如 ("APP", "server.http.addr") 得到 APP_SERVER_HTTP_ADDR。
func EnvName(prefix, path string) string {
	name := strings.ToUpper(strings.ReplaceAll(path, ".", "_"))
	if prefix == "" {
		return name
	}
	return strings.ToUpper(prefix) + "_" + name
}

func applyEnv(dst any, prefix string) error {
	for _, path := range Paths(dst) {
		raw, ok := os.LookupEnv(EnvName(prefix, path))
		if !ok {
			continue
		}
		if err := SetPath(dst, path, raw); err != nil {
			return err
		}
	}
	return nil
}

// Validate 检查配置结构体是否能被按路径寻址。指针字段、以及展开后不含任何
// 可寻址叶子的结构体字段（例如 time.Time），都会被报出来。
//
// 这类字段在环境变量与显式覆盖下会静默失效：Load 返回 nil，值却没被应用。
// 与其让人在线上才发现，不如启动即失败。
func Validate(dst any) error {
	v := reflect.ValueOf(dst)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return fmt.Errorf("config: dst 不能是空指针")
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("config: dst 必须指向结构体，得到 %s", v.Kind())
	}

	var problems []string
	checkAddressable(v, "", &problems)
	if len(problems) > 0 {
		return fmt.Errorf("config: 以下字段无法按路径寻址，请改用值类型的标量字段：%s",
			strings.Join(problems, "；"))
	}
	return nil
}

func checkAddressable(v reflect.Value, prefix string, problems *[]string) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.PkgPath != "" {
			continue
		}
		name := configName(sf)
		if name == "-" {
			continue
		}
		fv := v.Field(i)

		if isInline(sf) {
			if fv.Kind() == reflect.Struct {
				checkAddressable(fv, prefix, problems)
			}
			continue
		}

		path := name
		if prefix != "" {
			path = prefix + "." + name
		}

		switch {
		case fv.Kind() == reflect.Pointer:
			*problems = append(*problems, fmt.Sprintf("%s（指针字段）", path))
		case fv.Kind() == reflect.Struct && fv.Type() != durationType:
			var leaves []string
			collectPaths(fv, path, &leaves)
			if len(leaves) == 0 {
				*problems = append(*problems,
					fmt.Sprintf("%s（类型 %s 展开后没有可寻址字段）", path, fv.Type()))
				continue
			}
			checkAddressable(fv, path, problems)
		}
	}
}
