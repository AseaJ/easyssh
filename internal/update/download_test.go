package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpdateBinary 验证"下载 → sha256 校验 → 安装"全链路(用 httptest 模拟 GitHub release)。
func TestUpdateBinary(t *testing.T) {
	binData := []byte("MZ fake binary content for easyssh update test")
	hash := sha256.Sum256(binData)
	hashHex := hex.EncodeToString(hash[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/easyssh-windows-amd64.exe", func(w http.ResponseWriter, r *http.Request) {
		w.Write(binData)
	})
	mux.HandleFunc("/easyssh-linux-amd64", func(w http.ResponseWriter, r *http.Request) {
		w.Write(binData)
	})
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  easyssh-windows-amd64.exe\n%s  easyssh-linux-amd64\n", hashHex, hashHex)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rel := &Release{
		TagName: "v9.9.9",
		Assets: []Asset{
			{Name: "easyssh-windows-amd64.exe", BrowserDownloadURL: srv.URL + "/easyssh-windows-amd64.exe"},
			{Name: "easyssh-linux-amd64", BrowserDownloadURL: srv.URL + "/easyssh-linux-amd64"},
			{Name: "checksums.txt", BrowserDownloadURL: srv.URL + "/checksums.txt"},
		},
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "easyssh-new.exe")
	if err := UpdateBinary(context.Background(), rel, "v9.9.9", target); err != nil {
		t.Fatalf("UpdateBinary err: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("读取安装产物: %v", err)
	}
	if string(got) != string(binData) {
		t.Fatal("安装产物内容与下载不一致")
	}

	t.Run("篡改校验文件导致失败", func(t *testing.T) {
		dir2 := t.TempDir()
		target2 := filepath.Join(dir2, "easyssh-new.exe")
		// 篡改 checksums 服务(返回错误哈希)
		badMux := http.NewServeMux()
		badMux.HandleFunc("/easyssh-windows-amd64.exe", func(w http.ResponseWriter, r *http.Request) { w.Write(binData) })
		badMux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "0000000000000000000000000000000000000000000000000000000000000000  easyssh-windows-amd64.exe\n")
		})
		badSrv := httptest.NewServer(badMux)
		defer badSrv.Close()
		badRel := &Release{
			TagName: "v9.9.9",
			Assets: []Asset{
				{Name: "easyssh-windows-amd64.exe", BrowserDownloadURL: badSrv.URL + "/easyssh-windows-amd64.exe"},
				{Name: "checksums.txt", BrowserDownloadURL: badSrv.URL + "/checksums.txt"},
			},
		}
		err := UpdateBinary(context.Background(), badRel, "v9.9.9", target2)
		if err == nil || !strings.Contains(err.Error(), "sha256 校验失败") {
			t.Fatalf("期望 sha256 校验失败,got: %v", err)
		}
		if _, statErr := os.Stat(target2); statErr == nil {
			t.Fatal("校验失败后不应留下安装产物")
		}
	})

	t.Run("跨版本更新强制重新下载校验文件", func(t *testing.T) {
		// 第一次用 v9.9.9 成功更新;第二次 release 更新到 v10.0.0(新的 checksums 与二进制),
		// 即使目标目录残留旧 checksums.txt,也应强制重新下载新 checksums 并校验通过。
		v10Mux := http.NewServeMux()
		v10Bin := []byte("MZ v10 binary content")
		v10Hash := sha256.Sum256(v10Bin)
		v10HashHex := hex.EncodeToString(v10Hash[:])
		v10Mux.HandleFunc("/easyssh-windows-amd64.exe", func(w http.ResponseWriter, r *http.Request) { w.Write(v10Bin) })
		v10Mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "%s  easyssh-windows-amd64.exe\n", v10HashHex)
		})
		v10Srv := httptest.NewServer(v10Mux)
		defer v10Srv.Close()
		v10Rel := &Release{
			TagName: "v10.0.0",
			Assets: []Asset{
				{Name: "easyssh-windows-amd64.exe", BrowserDownloadURL: v10Srv.URL + "/easyssh-windows-amd64.exe"},
				{Name: "checksums.txt", BrowserDownloadURL: v10Srv.URL + "/checksums.txt"},
			},
		}
		// 第二次更新到同一 target(目录里已有 v9.9.9 时的 checksums 残留,内容不同)
		dir2 := t.TempDir()
		// 预置一个"旧版本"的 checksums.txt 残留(内容与 v10 不同),模拟跨版本场景
		oldChecksums := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff  easyssh-windows-amd64.exe\n"
		if err := os.WriteFile(filepath.Join(dir2, "checksums.txt"), []byte(oldChecksums), 0o644); err != nil {
			t.Fatal(err)
		}
		target2 := filepath.Join(dir2, "easyssh-new.exe")
		if err := UpdateBinary(context.Background(), v10Rel, "v10.0.0", target2); err != nil {
			t.Fatalf("跨版本更新应强制重下 checksums 并成功,got: %v", err)
		}
		got, err := os.ReadFile(target2)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(v10Bin) {
			t.Fatal("安装的是旧二进制")
		}
	})

	t.Run("缺少平台资产", func(t *testing.T) {
		dir3 := t.TempDir()
		target3 := filepath.Join(dir3, "easyssh-new.exe")
		rel3 := &Release{TagName: "v9.9.9", Assets: []Asset{{Name: "other.bin", BrowserDownloadURL: srv.URL + "/other.bin"}}}
		if err := UpdateBinary(context.Background(), rel3, "v9.9.9", target3); err == nil {
			t.Fatal("期望缺少平台资产时报错")
		}
	})
}
