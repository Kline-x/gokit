package config

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

var durationType = reflect.TypeOf(time.Duration(0))

// SetPath 按点分路径设置结构体字段，例如 SetPath(&cfg, "server.http.addr", ":80")。
// 路径的每一段对应字段的 yaml 标签名，无标签时取字段名的小写形式。
func SetPath(dst any, path, value string) error {
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return fmt.Errorf("config: dst 必须是非空指针，得到 %T", dst)
	}
	v = v.Elem()

	for _, part := range strings.Split(path, ".") {
		if v.Kind() != reflect.Struct {
			return fmt.Errorf("config: 路径 %q 中的 %q 不是结构体字段", path, part)
		}
		f, ok := fieldByConfigName(v, part)
		if !ok {
			return fmt.Errorf("config: 未知配置路径 %q", path)
		}
		v = f
	}
	if err := setScalar(v, value); err != nil {
		return fmt.Errorf("config: 设置 %q 失败: %w", path, err)
	}
	return nil
}

func fieldByConfigName(v reflect.Value, name string) (reflect.Value, bool) {
	t := v.Type()
	// 第一遍：只看非内联字段。
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.PkgPath != "" || isInline(sf) {
			continue
		}
		cn := configName(sf)
		if cn == "-" || cn != name {
			continue
		}
		return v.Field(i), true
	}
	// 第二遍：内联字段的键在父级展开，递归进去找。
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.PkgPath != "" || !isInline(sf) {
			continue
		}
		fv := v.Field(i)
		if fv.Kind() != reflect.Struct {
			continue
		}
		if f, ok := fieldByConfigName(fv, name); ok {
			return f, true
		}
	}
	return reflect.Value{}, false
}

// isInline 判断字段是否带 yaml 的 inline 选项。内联字段的键在父级展开，
// 因此它的路径也应当沿用父前缀，而不是多出一层字段名。
func isInline(sf reflect.StructField) bool {
	opts := strings.Split(sf.Tag.Get("yaml"), ",")
	for _, opt := range opts[1:] {
		if strings.TrimSpace(opt) == "inline" {
			return true
		}
	}
	return false
}

func configName(sf reflect.StructField) string {
	tag := sf.Tag.Get("yaml")
	if tag == "" {
		return strings.ToLower(sf.Name)
	}
	name := strings.Split(tag, ",")[0]
	if name == "" {
		return strings.ToLower(sf.Name)
	}
	return name
}

func setScalar(v reflect.Value, s string) error {
	if !v.CanSet() {
		return fmt.Errorf("字段不可写")
	}
	if v.Type() == durationType {
		d, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("无法解析时长 %q: %w", s, err)
		}
		v.SetInt(int64(d))
		return nil
	}

	switch v.Kind() {
	case reflect.String:
		v.SetString(s)
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("无法解析布尔值 %q: %w", s, err)
		}
		v.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("无法解析整数 %q: %w", s, err)
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(s, 10, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("无法解析无符号整数 %q: %w", s, err)
		}
		v.SetUint(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(s, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("无法解析浮点数 %q: %w", s, err)
		}
		v.SetFloat(f)
	case reflect.Slice:
		if v.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("只支持字符串切片，得到 %s", v.Type())
		}
		if strings.TrimSpace(s) == "" {
			v.Set(reflect.MakeSlice(v.Type(), 0, 0))
			return nil
		}
		parts := strings.Split(s, ",")
		// 逐个 SetString 而不是直接赋一个 []string：元素类型可能是底层为 string
		// 的具名类型，那时 []string 并不可直接赋值过去。
		out := reflect.MakeSlice(v.Type(), len(parts), len(parts))
		for i, p := range parts {
			out.Index(i).SetString(strings.TrimSpace(p))
		}
		v.Set(out)
	default:
		return fmt.Errorf("不支持的字段类型 %s", v.Type())
	}
	return nil
}
