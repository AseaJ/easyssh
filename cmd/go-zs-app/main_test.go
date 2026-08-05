package main

import (
	"os"
	"path/filepath"
	"testing"
)

// makeLayout 构造模拟真实布局:<root>/cmd/go-zs-app/build/bin 为 exe 目录,root 下放 go.mod。
// wantCfg 为 true 时在 root 下放 go-zs.yaml。
func makeLayout(t *testing.T, wantCfg bool) (root, bin string) {
	t.Helper()
	dir := t.TempDir()
	root = filepath.Join(dir, "root")
	bin = filepath.Join(root, "cmd", "go-zs-app", "build", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if wantCfg {
		if err := os.WriteFile(filepath.Join(root, "go-zs.yaml"), []byte("certificates: []\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, bin
}

// 桌面端从 build/bin 启动、项目根有配置:应向上找到项目根配置。
func TestFindConfigDirFromProjectRoot(t *testing.T) {
	root, bin := makeLayout(t, true)
	if got := findConfigDirFrom(bin, t.TempDir()); got != root {
		t.Errorf("findConfigDirFrom(%s) = %s,期望项目根 %s", bin, got, root)
	}
}

// exe 所在目录有配置:应优先用 exe 目录(比项目根更高优先级)。
func TestFindConfigDirPrefersExeDir(t *testing.T) {
	_, bin := makeLayout(t, true)
	if err := os.WriteFile(filepath.Join(bin, "go-zs.yaml"), []byte("certificates: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := findConfigDirFrom(bin, t.TempDir()); got != bin {
		t.Errorf("exe 目录有配置时应优先,实际 %s", got)
	}
}

// 工作目录有配置:兜底使用。
func TestFindConfigDirFallsBackToCwd(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go-zs.yaml"), []byte("certificates: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := findConfigDirFrom(filepath.Join(dir, "no-such-exe"), dir); got != dir {
		t.Errorf("兜底应为 cwd,实际 %s", got)
	}
}

// 项目根有 go.mod 但无配置:应跳过,继续向上;最终兜底 cwd。
func TestFindConfigDirSkipsRootWithoutConfig(t *testing.T) {
	root, bin := makeLayout(t, false)
	cwd := t.TempDir()
	if got := findConfigDirFrom(bin, cwd); got != cwd {
		t.Errorf("项目根无配置时兜底 cwd,实际 %s", got)
	}
	_ = root
}
