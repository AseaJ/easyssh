// Package storage 定义证书存储抽象,默认实现为本地文件系统。
package storage

import (
	"context"

	"github.com/asea/easyssh/internal/certmgr"
)

// Store 是证书/私钥/元数据的存储抽象。
type Store interface {
	// Save 原子保存证书包(临时文件 + rename),并更新 meta.json。
	Save(ctx context.Context, bundle *certmgr.Bundle) error
	// Load 读取指定条目的证书包。
	Load(ctx context.Context, name string) (*certmgr.Bundle, error)
	// List 返回全部已托管条目。
	List(ctx context.Context) ([]*certmgr.Bundle, error)
}
