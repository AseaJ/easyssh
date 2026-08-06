//go:build !windows

package app

import "context"

// 非 Windows 平台无托盘支持,提供编译期 stub。

type trayIcon struct{}

type trayMenu int

const (
	trayNone trayMenu = iota
	trayShow
	trayQuit
)

func traySupported() bool { return false }

func newTrayIcon(onAction chan trayMenu) (*trayIcon, error) { return nil, nil }

func (t *trayIcon) Stop() {}

func showMainWindow(a *App) {}

func quitApplication(a *App) {}

// cacheMainWindow 非 Windows 平台无托盘,空实现。
func cacheMainWindow() uintptr { return 0 }

// wailsQuit 非 Windows 平台 stub(Startup 里自启失败退出路径引用,空实现)。
func wailsQuit(ctx context.Context) {}

// InjectWailsHooks 非 Windows 平台无托盘,注入为空实现。
func InjectWailsHooks(show, hide, quit func(ctx context.Context)) {}
