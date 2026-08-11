package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// AssetName 返回当前平台对应的 CLI 二进制资产名(与 release.yml 交叉编译产物命名一致)。
func AssetName() string {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("easyssh-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)
}

// ChecksumsName 返回校验文件资产名。
func ChecksumsName() string { return "checksums.txt" }

// FindAsset 在 release 资产列表中查找指定名称的资产。
func FindAsset(rel *Release, name string) (Asset, bool) {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// VerifySHA256 校验 data 的 sha256 是否与 checksums 中该文件名对应的值一致。
// checksums 内容形如 "<sha256>  <filename>" 每行一条(与 release.yml 生成的 checksums.txt 一致)。
func VerifySHA256(checksums []byte, filename string, data []byte) error {
	want := checksumFor(checksums, filename)
	if want == "" {
		return fmt.Errorf("checksums.txt 中未找到 %s 的校验值", filename)
	}
	got := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(got[:]), want) {
		return fmt.Errorf("%s sha256 校验失败(期望 %s,实际 %s)", filename, want, hex.EncodeToString(got[:]))
	}
	return nil
}

// checksumFor 解析 checksums 文本,返回 filename 对应的十六进制 sha256(小写)。
func checksumFor(checksums []byte, filename string) string {
	for _, line := range strings.Split(string(checksums), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[1] == filename {
			return strings.ToLower(fields[0])
		}
	}
	return ""
}

// Download 下载 URL 到目标路径(原子写入:先写 .tmp 再改名)。
// force=true 时即使目标已存在也重新下载(用于每次更新需重新拉取的最新校验文件);
// force=false 时目标已存在且非空则跳过(幂等)。
func Download(ctx context.Context, url, target string, force bool) error {
	if !force {
		if fi, err := os.Stat(target); err == nil {
			// 目标已存在且非空:视为已下载完成,跳过(避免重复下载)
			if fi.Size() > 0 {
				return nil
			}
		}
	}
	tmp := target + fmt.Sprintf(".tmp%d", time.Now().UnixNano())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "easyssh-update")
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// 读一部分响应体用于错误信息(如 404 页面)
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("下载失败: HTTP %s(%s)", resp.Status, strings.TrimSpace(string(body)))
	}
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("创建临时文件: %w", err)
	}
	_, err = io.Copy(out, resp.Body)
	cerr := out.Close()
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("写入文件失败: %w", err)
	}
	if cerr != nil {
		os.Remove(tmp)
		return fmt.Errorf("关闭文件失败: %w", cerr)
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("移动文件失败: %w", err)
	}
	return nil
}

// UpdateBinary 下载新版本二进制并校验 sha256,写入 target。
// rel 为已获取的 release 信息,tag 为原始 tag(用于拼下载 URL)。
// 每次调用都会强制重新下载校验文件(force=true),避免旧版本残留的 checksums.txt
// 导致跨版本更新校验失败;二进制用唯一临时名,校验通过后再替换。
func UpdateBinary(ctx context.Context, rel *Release, tag, target string) error {
	assetName := AssetName()
	asset, ok := FindAsset(rel, assetName)
	if !ok {
		return fmt.Errorf("最新版本 %s 未提供当前平台二进制 %s", tag, assetName)
	}
	// 先下载校验文件,再下载二进制
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("创建目标目录: %w", err)
	}
	checksumAsset, ok := FindAsset(rel, ChecksumsName())
	if !ok {
		return fmt.Errorf("最新版本 %s 缺少校验文件 %s", tag, ChecksumsName())
	}
	// 校验文件用唯一临时名下载,读入内存校验,不污染目标目录
	checksumsTmp := filepath.Join(filepath.Dir(target), fmt.Sprintf(".checksums.%d.tmp", time.Now().UnixNano()))
	defer os.Remove(checksumsTmp)
	if err := Download(ctx, checksumAsset.BrowserDownloadURL, checksumsTmp, true); err != nil {
		return fmt.Errorf("下载校验文件失败: %w", err)
	}
	checksums, err := os.ReadFile(checksumsTmp)
	if err != nil {
		return fmt.Errorf("读取校验文件失败: %w", err)
	}

	// 下载二进制到唯一临时文件再校验,避免校验失败污染目标位置
	tmp := target + fmt.Sprintf(".download.%d", time.Now().UnixNano())
	defer os.Remove(tmp)
	if err := Download(ctx, asset.BrowserDownloadURL, tmp, true); err != nil {
		return fmt.Errorf("下载新版本失败: %w", err)
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		return fmt.Errorf("读取下载内容失败: %w", err)
	}
	if err := VerifySHA256(checksums, assetName, data); err != nil {
		return fmt.Errorf("校验失败: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("安装新版本失败: %w", err)
	}
	return nil
}
