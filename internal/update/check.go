// Package update 提供版本更新检测与下载能力(CLI 与 GUI 共用)。
//
// 检测数据源为 GitHub Releases 的 latest 版本(https://api.github.com/repos/AseaJ/easyssh/releases/latest),
// 与当前程序版本(构建期注入,形如 v0.2.1)比较,发现新版本时给出提示;
// CLI 的 `easyssh update` 子命令进一步下载并校验对应平台的二进制(sha256 与 checksums.txt 核对)。
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// GitHub 仓库信息(latest release 的检测数据源)。
const (
	RepoOwner   = "AseaJ"
	RepoName    = "easyssh"
	ReleasesURL = "https://github.com/" + RepoOwner + "/" + RepoName + "/releases"
)

// LatestAPI 返回仓库最新 release 的 JSON(不经鉴权,速率限制 60 次/小时/IP)。
// 声明为 var 便于测试替换为本地 httptest 地址。
var LatestAPI = "https://api.github.com/repos/" + RepoOwner + "/" + RepoName + "/releases/latest"

// HTTPClient 供测试替换的超时客户端(默认 10 秒)。
var HTTPClient = &http.Client{Timeout: 10 * time.Second}

// Release 是 GitHub release API 响应的最小子集(仅用到字段)。
type Release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	Assets      []Asset   `json:"assets"`
}

// Asset 是 release 资产(可下载的二进制/校验文件)。
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Info 是版本检测的结果。
type Info struct {
	Current    string // 当前程序版本(可能为空=未知)
	Latest     string // 远端最新版本 tag
	ReleaseURL string // 最新版本发布页 URL
	HasUpdate  bool   // 远端是否有更新版本
	Error      string `json:"error,omitempty"` // 检测失败原因(如无网络),非致命
}

// LatestRelease 获取仓库最新 release 的完整信息(含 assets 列表)。
func LatestRelease(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, LatestAPI, nil)
	if err != nil {
		return nil, err
	}
	// 指定版本,避免 API 返回的默认资产名差异;也便于服务端统计客户端
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "easyssh-update-check")
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitHub 最新版本失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// 403 多为匿名 API 限流(60 次/小时/IP),给出可操作提示
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			return nil, fmt.Errorf("GitHub 请求过于频繁(%s),请稍后重试", resp.Status)
		}
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("GitHub 仓库或 release 不存在(%s)", resp.Status)
		}
		return nil, fmt.Errorf("GitHub API 返回 %s", resp.Status)
	}
	var rel Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("解析 GitHub 响应失败: %w", err)
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("GitHub 响应缺少 tag_name")
	}
	if rel.HTMLURL == "" {
		rel.HTMLURL = ReleasesURL
	}
	return &rel, nil
}

// Latest 获取远端最新 release 并对比当前版本。
// current 形如 v0.2.1(内部统一补 v 前缀);网络失败等错误返回 error,
// 调用方应将其视为"提示失败"而非程序错误。
func Latest(ctx context.Context, current string) (*Info, error) {
	rel, err := LatestRelease(ctx)
	if err != nil {
		return nil, err
	}
	info := &Info{
		Current:    NormalizeVersion(current),
		Latest:     NormalizeVersion(rel.TagName),
		ReleaseURL: rel.HTMLURL,
	}
	if info.ReleaseURL == "" {
		info.ReleaseURL = ReleasesURL
	}
	// 语义化版本比较:新版更大即为有更新;当前版本未知(空)或非法(非 semver)时不提示。
	// 用 normalizeForCompare 补全(v0.2 → v0.2.0)后再校验,兼容简写版本。
	curNorm := normalizeForCompare(info.Current)
	info.HasUpdate = info.Current != "" && semver.IsValid(curNorm) && CompareVersions(info.Latest, info.Current) > 0
	return info, nil
}

// CompareVersions 比较两个版本号,返回 -1/0/+1。
// 版本号先归一化(v 前缀 + 补全 x.y.z),再做语义化比较;非法版本视为最小。
func CompareVersions(a, b string) int {
	return semverCompare(a, b)
}

// NormalizeVersion 将版本号归一化为语义化版本(v0.2.1 → v0.2.1;0.2.1 → v0.2.1)。
// 带 v 前缀时补 0(如 v0.2 → v0.2.0),便于 semver 比较。
func NormalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}
