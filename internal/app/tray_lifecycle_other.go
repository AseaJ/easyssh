//go:build !windows

package app

import "context"

// BeforeClose 非 Windows 平台无托盘,不拦截关窗(直接放行)。
func (a *App) BeforeClose(ctx context.Context) bool { return false }

// OnShutdown 非 Windows 平台无托盘,无清理。
func (a *App) OnShutdown(ctx context.Context) {}

// HideToTray 非 Windows 平台无托盘,空实现。
func (a *App) HideToTray() {}

// QuitFromTray 非 Windows 平台无托盘,空实现。
func (a *App) QuitFromTray() {}
