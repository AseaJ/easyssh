package config

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
)

var envRefRe = regexp.MustCompile(`\{\{env:([A-Za-z_][A-Za-z0-9_]*)\}\}`)

// ExpandEnvRefs 递归展开配置中所有字符串字段的 {{env:VAR}} 引用。
// 引用的环境变量缺失时返回错误,避免把空密钥带进签发流程。
func ExpandEnvRefs(cfg *Config) error {
	v := reflect.ValueOf(cfg).Elem()
	return expandValue(v)
}

func expandValue(v reflect.Value) error {
	switch v.Kind() {
	case reflect.String:
		s := v.String()
		expanded, err := expandString(s)
		if err != nil {
			return err
		}
		if expanded != s {
			v.SetString(expanded)
		}
	case reflect.Ptr:
		if !v.IsNil() {
			return expandValue(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if err := expandValue(v.Field(i)); err != nil {
				return err
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if err := expandValue(v.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			mv := iter.Value()
			if mv.Kind() == reflect.Interface {
				mv = mv.Elem()
			}
			if mv.Kind() == reflect.String {
				expanded, err := expandString(mv.String())
				if err != nil {
					return err
				}
				v.SetMapIndex(iter.Key(), reflect.ValueOf(expanded))
			}
		}
	}
	return nil
}

func expandString(s string) (string, error) {
	if !envRefRe.MatchString(s) {
		return s, nil
	}
	var errs []string
	out := envRefRe.ReplaceAllStringFunc(s, func(m string) string {
		name := envRefRe.FindStringSubmatch(m)[1]
		val, ok := os.LookupEnv(name)
		if !ok {
			errs = append(errs, name)
			return m
		}
		return val
	})
	if len(errs) > 0 {
		return s, fmt.Errorf("环境变量未设置: %s", errs)
	}
	return out, nil
}

// expandEnvRefsLenient 与 ExpandEnvRefs 相同,但缺失的环境变量保留引用并返回其名称。
func expandEnvRefsLenient(cfg *Config) ([]string, error) {
	var missing []string
	v := reflect.ValueOf(cfg).Elem()
	err := expandValueLenient(v, &missing)
	return missing, err
}

func expandValueLenient(v reflect.Value, missing *[]string) error {
	switch v.Kind() {
	case reflect.String:
		s := v.String()
		expanded, m, err := expandStringLenient(s)
		if err != nil {
			return err
		}
		*missing = append(*missing, m...)
		if expanded != s {
			v.SetString(expanded)
		}
	case reflect.Ptr:
		if !v.IsNil() {
			return expandValueLenient(v.Elem(), missing)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if err := expandValueLenient(v.Field(i), missing); err != nil {
				return err
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if err := expandValueLenient(v.Index(i), missing); err != nil {
				return err
			}
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			mv := iter.Value()
			if mv.Kind() == reflect.Interface {
				mv = mv.Elem()
			}
			if mv.Kind() == reflect.String {
				expanded, m, err := expandStringLenient(mv.String())
				if err != nil {
					return err
				}
				*missing = append(*missing, m...)
				v.SetMapIndex(iter.Key(), reflect.ValueOf(expanded))
			}
		}
	}
	return nil
}

// expandStringLenient 展开引用;缺失的环境变量保留原文并返回其名称。
func expandStringLenient(s string) (string, []string, error) {
	if !envRefRe.MatchString(s) {
		return s, nil, nil
	}
	var missing []string
	out := envRefRe.ReplaceAllStringFunc(s, func(m string) string {
		name := envRefRe.FindStringSubmatch(m)[1]
		val, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
			return m // 保留引用,稍后由 GUI 提示补齐
		}
		return val
	})
	return out, missing, nil
}
