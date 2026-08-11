package update

import (
	"strings"

	"golang.org/x/mod/semver"
)

// semverCompare 用 golang.org/x/mod/semver 比较两个版本号(兼容不完整写法,如 v0.2)。
// 两个版本先被归一化补全(v0.2 → v0.2.0),再做语义化比较;
// 非法版本视为最小(比任何合法版本都小),符合 semver 包约定。
func semverCompare(a, b string) int {
	na := normalizeForCompare(a)
	nb := normalizeForCompare(b)
	if na == "" || nb == "" {
		switch {
		case na == "" && nb == "":
			return 0
		case na == "":
			return -1
		default:
			return 1
		}
	}
	return semver.Compare(na, nb)
}

// normalizeForCompare 将版本号补全为 x/mod/semver 可解析的形式:
//   - 去掉 v 前缀按 . 拆分主版本号段,不足 3 段补 0(v0.2 → v0.2.0)
//   - 预发布段(-beta 等)与构建元数据(+build)原样保留
//   - 空串返回空串
func normalizeForCompare(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	v = strings.TrimPrefix(v, "v")
	// 拆预发布/构建元数据(两者都可能含 . 或 +,-)
	core := v
	suffix := ""
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		core, suffix = v[:i], v[i:]
	}
	nums := strings.Split(core, ".")
	for len(nums) < 3 {
		nums = append(nums, "0")
	}
	return "v" + strings.Join(nums, ".") + suffix
}
