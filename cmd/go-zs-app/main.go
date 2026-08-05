// Command go-zs-app 是 go-zs 的桌面应用入口(Wails)。
package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"

	legoLog "github.com/go-acme/lego/v4/log"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"go-zs/internal/app"

	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	configDir, err := resolveConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	a := app.NewApp(configDir)
	// 收集日志到 GUI 日志面板:标准库 logger + lego 内部 logger
	log.SetOutput(a.LogWriter())
	legoLog.Logger = log.New(a.LogWriter(), "", log.LstdFlags)

	err = wails.Run(&options.App{
		Title:     "go-zs 证书托管",
		Width:     1160,
		Height:    780,
		MinWidth:  920,
		MinHeight: 640,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// 与前端暗色背景同步;前端会按自身主题渲染,此处仅作启动底色
		BackgroundColour: &options.RGBA{R: 30, G: 30, B: 30, A: 1},
		Windows: &windows.Options{
			Theme:            windows.SystemDefault, // 跟随系统深浅色
			WebviewIsTransparent: false,
			// Windows 11 22621+:窗口背景用 Mica 材质,与前端毛玻璃面板呼应
			BackdropType: windows.Mica,
		},
		OnStartup: a.Startup,
		Bind: []interface{}{
			a,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// resolveConfigDir 按优先级确定配置文件目录:
//  1. 环境变量 GOZS_CONFIG_DIR(显式指定)
//  2. exe 所在目录(存在 go-zs.yaml)
//  3. 从 exe 目录向上找项目根(含 go.mod 且存在 go-zs.yaml)——桌面端从 build/bin 启动时的常规场景
//  4. 当前工作目录(存在 go-zs.yaml)
//  5. 当前工作目录(兜底,启动后由 App 报错提示)
func resolveConfigDir() (string, error) {
	if v := os.Getenv("GOZS_CONFIG_DIR"); v != "" {
		abs, err := filepath.Abs(v)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return findConfigDirFrom(exeDir, cwd), nil
}

// findConfigDirFrom 核心解析逻辑(便于测试):exeDir 为 exe 所在目录,cwd 为工作目录。
func findConfigDirFrom(exeDir, cwd string) string {
	if exeDir != "" {
		if _, err := os.Stat(filepath.Join(exeDir, "go-zs.yaml")); err == nil {
			return exeDir
		}
		if root := findProjectRootConfig(exeDir); root != "" {
			return root
		}
	}
	if _, err := os.Stat(filepath.Join(cwd, "go-zs.yaml")); err == nil {
		return cwd
	}
	return cwd
}

// findProjectRootConfig 从 dir 向上(最多 8 层)找第一个含 go.mod 且存在 go-zs.yaml 的目录。
func findProjectRootConfig(dir string) string {
	d := dir
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(d, "go-zs.yaml")); err == nil {
				return d
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return ""
}
