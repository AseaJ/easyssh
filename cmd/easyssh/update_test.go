package main

import (
	"strings"
	"testing"

	"github.com/asea/easyssh/internal/update"
)

func TestBuildUpdateScript(t *testing.T) {
	oldExe := `C:\tools\easyssh.exe`
	newExe := `C:\tools\easyssh.update.exe`
	s := buildUpdateScript(oldExe, newExe)
	checks := []string{
		`@echo off`,
		`cd /d "C:\tools"`,
		`tasklist /FI "IMAGENAME eq easyssh.exe"`,
		`ren "C:\tools\easyssh.exe" easyssh.old.exe`,
		`move /y "C:\tools\easyssh.update.exe" "C:\tools\easyssh.exe"`,
		`if errorlevel 1 goto :fail`,
		`del /f /q "%~f0"`,
	}
	for _, want := range checks {
		if !strings.Contains(s, want) {
			t.Errorf("脚本缺少 %q\n实际:\n%s", want, s)
		}
	}
	// 不应引用未转义的绝对路径
	if strings.Contains(s, `C:\tools`) == false {
		t.Errorf("脚本应包含安装目录: %s", s)
	}
}

func TestCmdEscape(t *testing.T) {
	// 含 cmd 元字符的路径必须被转义,防止注入
	path := `C:\Users\John & Mary\My (Files)\easyssh.exe`
	esc := cmdEscape(path)
	// & ( ) 应被 ^ 转义;% 应变为 %%(批处理中成对)
	for _, ch := range []string{`&`, `(`, `)`} {
		if !strings.Contains(esc, "^"+ch) {
			t.Errorf("cmdEscape 未转义 %q: %q", ch, esc)
		}
	}
	// 转义后的字符串不应含裸 & | < >(未跟随 ^)
	for _, bare := range []string{` & `, ` | `, ` < `, ` > `, `" & "`} {
		if strings.Contains(esc, bare) {
			t.Errorf("cmdEscape 残留裸元字符 %q: %q", bare, esc)
		}
	}
}

func TestBuildUpdateScriptEscapesMetachars(t *testing.T) {
	// 含 & 的路径:注入尝试应被转义,脚本中 & 前有 ^
	oldExe := `C:\Users\John & Mary\easyssh.exe`
	newExe := `C:\Users\John & Mary\easyssh.update.exe`
	s := buildUpdateScript(oldExe, newExe)
	if !strings.Contains(s, `John ^& Mary`) {
		t.Errorf("脚本未转义 & 字符:\n%s", s)
	}
	if strings.Contains(s, `John & Mary`) {
		t.Errorf("脚本包含未转义的 &(可注入命令):\n%s", s)
	}
}

func TestConfirm(t *testing.T) {
	// 空输入(管道关闭)返回 false,不阻塞
	if confirm("测试确认?") {
		t.Fatal("空输入应返回 false")
	}
}

func TestDoUpdateNoAssets(t *testing.T) {
	// 验证 FindAsset 对缺失资产的判断(doUpdate 依赖此分支报错)
	rel := &update.Release{
		TagName: "v9.9.9",
		Assets:  []update.Asset{{Name: "other.bin", BrowserDownloadURL: "https://example.com/other.bin"}},
	}
	if _, ok := update.FindAsset(rel, update.AssetName()); ok {
		t.Fatal("缺失当前平台资产时 FindAsset 应返回 false")
	}
}
