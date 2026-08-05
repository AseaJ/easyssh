package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildRestartScript(t *testing.T) {
	oldExe := `C:\easyssh\build\bin\easyssh.exe`
	newExe := `C:\easyssh\build\bin\easyssh-new.exe`
	s := buildRestartScript(oldExe, newExe)
	for _, want := range []string{
		`tasklist /FI "IMAGENAME eq easyssh.exe"`,
		`del /f /q "C:\easyssh\build\bin\easyssh.exe"`,
		`move /y "C:\easyssh\build\bin\easyssh-new.exe" "C:\easyssh\build\bin\easyssh.exe"`,
		`start "" "C:\easyssh\build\bin\easyssh.exe"`,
		`del /f /q "%~f0"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("脚本缺少 %q:\n%s", want, s)
		}
	}
}

func TestFindProjectRoot(t *testing.T) {
	// 模拟真实布局:exe 在 <root>/cmd/easyssh-app/build/bin
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	bin := filepath.Join(root, "cmd", "easyssh-app", "build", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cmd", "easyssh-app", "wails.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 把当前 exe 指向模拟布局:os.Executable 不可注入,改用 findProjectRootFrom 直接测目录扫描逻辑
	got := findProjectRootFrom(bin)
	if got != root {
		t.Errorf("findProjectRootFrom(%s) = %s,期望 %s", bin, got, root)
	}
}

func TestTailString(t *testing.T) {
	if got := tailString("hello world", 5); got != "…(截断)…world" {
		t.Errorf("tailString 截断异常: %q", got)
	}
	if got := tailString("hi", 10); got != "hi" {
		t.Errorf("tailString 短串异常: %q", got)
	}
}

func TestEnsureNewExe(t *testing.T) {
	dir := t.TempDir()
	built := filepath.Join(dir, "built", "easyssh-new.exe")
	if err := os.MkdirAll(filepath.Dir(built), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(built, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	// 同路径:不复制、不报错
	if err := ensureNewExe(built, built); err != nil {
		t.Errorf("同路径应直接返回: %v", err)
	}

	// 不同路径:复制到 exe 目录
	target := filepath.Join(dir, "bin", "easyssh-new.exe")
	if err := ensureNewExe(built, target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "binary" {
		t.Errorf("复制内容 = %q,期望 binary", got)
	}

	// 源不存在:应报错
	if err := ensureNewExe(filepath.Join(dir, "missing.exe"), target); err == nil {
		t.Error("源文件不存在应报错")
	}
}
