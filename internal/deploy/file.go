package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go-zs/internal/certmgr"
)

// FileConfig 是 file 部署目标配置:把证书复制到指定目录。
type FileConfig struct {
	Dir    string
	Runner CommandRunner // 保留字段,file 部署不需要外部命令
}

// File 把 fullchain/privkey 复制到目标目录。
type File struct {
	cfg FileConfig
}

func NewFile(cfg FileConfig) (*File, error) {
	if cfg.Dir == "" {
		return nil, errors.New("file 部署需要 dir")
	}
	return &File{cfg: cfg}, nil
}

func (f *File) Name() string { return "file" }

func (f *File) Deploy(ctx context.Context, bundle *certmgr.Bundle) error {
	if bundle == nil || len(bundle.FullchainPEM) == 0 || len(bundle.PrivateKeyPEM) == 0 {
		return errors.New("证书包不完整")
	}
	if bundle.Meta.DeployedFingerprint == bundle.Fingerprint &&
		contains(bundle.Meta.DeployedTargets, f.Name()) {
		return nil
	}
	if err := os.MkdirAll(f.cfg.Dir, 0o755); err != nil {
		return err
	}
	if err := atomicWriteFile(filepath.Join(f.cfg.Dir, "fullchain.pem"), bundle.FullchainPEM, 0o644); err != nil {
		return fmt.Errorf("写入 fullchain.pem: %w", err)
	}
	if err := atomicWriteFile(filepath.Join(f.cfg.Dir, "privkey.pem"), bundle.PrivateKeyPEM, 0o600); err != nil {
		return fmt.Errorf("写入 privkey.pem: %w", err)
	}
	return nil
}
