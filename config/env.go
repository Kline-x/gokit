package config

import (
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
		name := configName(sf)
		if name == "-" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		fv := v.Field(i)
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
