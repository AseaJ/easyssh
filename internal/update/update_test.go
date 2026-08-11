package update

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.2.1", "v0.2.1", 0},
		{"0.2.1", "v0.2.1", 0},
		{"v0.2.1", "v0.3.0", -1},
		{"v0.3.0", "v0.2.1", 1},
		{"v0.2", "v0.2.0", 0},
		{"v0.2.1", "v0.2.10", -1},
		{"v1.0.0", "v0.9.9", 1},
		{"v0.2.1-beta.1", "v0.2.1", -1},
		{"", "v0.2.1", -1},
		{"v0.2.1", "", 1},
		{"", "", 0},
		{"abc", "v0.2.1", -1}, // 非法版本视为最小
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestNormalizeVersion(t *testing.T) {
	cases := []struct{ in, want string }{
		{"v0.2.1", "v0.2.1"},
		{"0.2.1", "v0.2.1"},
		{" v0.2.1 ", "v0.2.1"},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeVersion(c.in); got != c.want {
			t.Errorf("NormalizeVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestChecksumFor(t *testing.T) {
	checksums := []byte(`33040059d689c6c37014da5fb83b1d2d6da51abf9536aa41f88bb7fd40d3d5f0  checksums.txt
ecc09e4c492eb5e66146551046688a4ed1190d04459f288f415ae3502c19c7b0  easyssh-linux-amd64
8c62a25dd4aad46f7fd8ca2bb54c34e11de2a88d0b172e24398ae372fedb1455  easyssh-windows-amd64.exe
`)
	if got := checksumFor(checksums, "easyssh-windows-amd64.exe"); got != "8c62a25dd4aad46f7fd8ca2bb54c34e11de2a88d0b172e24398ae372fedb1455" {
		t.Errorf("checksumFor(windows) = %q", got)
	}
	if got := checksumFor(checksums, "easyssh-linux-amd64"); got != "ecc09e4c492eb5e66146551046688a4ed1190d04459f288f415ae3502c19c7b0" {
		t.Errorf("checksumFor(linux) = %q", got)
	}
	if got := checksumFor(checksums, "not-exist"); got != "" {
		t.Errorf("checksumFor(not-exist) = %q, want empty", got)
	}
}

func TestVerifySHA256(t *testing.T) {
	data := []byte("hello easyssh")
	checksums := []byte("a1f0e8e1b5e8d7c8b8a4a0b7d9e6f5a1a1f0e8e1b5e8d7c8b8a4a0b7d9e6f5a1  easyssh-fake.exe\n")
	// 错误值:重新计算正确值
	if err := VerifySHA256(checksums, "easyssh-fake.exe", data); err == nil {
		t.Fatal("期望校验失败,实际成功")
	}
	// 构造正确值
	imported := sha256hex(data)
	ok := []byte(imported + "  easyssh-fake.exe\n")
	if err := VerifySHA256(ok, "easyssh-fake.exe", data); err != nil {
		t.Fatalf("期望校验成功,实际失败: %v", err)
	}
	if err := VerifySHA256(ok, "missing-file", data); err == nil {
		t.Fatal("期望缺失文件名时报错")
	}
}

func sha256hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
