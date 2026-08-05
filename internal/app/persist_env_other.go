//go:build !windows

package app

import "os"

// PersistEnv 把密钥写入系统环境变量并持久化(Windows 写注册表,其他平台仅进程内)。
// 供 CLI import 与 bundle 导入回调复用。
func PersistEnv(name, value string) error {
	if err := os.Setenv(name, value); err != nil {
		return err
	}
	return persistEnvPlatform(name, value)
}

// persistEnvPlatform 非 Windows 平台:仅当前进程生效(调用方已 os.Setenv)。
func persistEnvPlatform(name, value string) error {
	return nil
}

// LoadPersistedEnv 非 Windows 平台:无注册表,空实现。
func LoadPersistedEnv() {}
