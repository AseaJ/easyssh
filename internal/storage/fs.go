package storage

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"go-zs/internal/certmgr"
)

// 磁盘布局(storage.dir 即条目产物目录):
//
//	<dir>/
//	├── cert.pem        # 仅 leaf
//	├── fullchain.pem   # leaf + intermediate
//	├── chain.pem       # 仅 intermediate
//	├── privkey.pem     # 私钥,0600
//	└── meta.json       # 状态(JSON)

const (
	fileCert      = "cert.pem"
	fileFullchain = "fullchain.pem"
	fileChain     = "chain.pem"
	filePrivkey   = "privkey.pem"
	fileMeta      = "meta.json"

	metaPerm = os.FileMode(0o644)
	keyPerm  = os.FileMode(0o600)
	certPerm = os.FileMode(0o644)
	dirPerm  = os.FileMode(0o700)
)

// FS 是 Store 的本地文件系统实现。一个 FS 实例对应一个条目目录(storage.dir)。
type FS struct {
	dir string
}

func NewFS(dir string) (*FS, error) {
	if dir == "" {
		return nil, errors.New("存储目录不能为空")
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("创建存储目录 %s: %w", dir, err)
	}
	return &FS{dir: dir}, nil
}

// Save 原子保存证书包:先写临时文件再 rename,meta.json 最后写入。
func (f *FS) Save(_ context.Context, b *certmgr.Bundle) error {
	if err := os.MkdirAll(f.dir, dirPerm); err != nil {
		return fmt.Errorf("创建条目目录: %w", err)
	}
	// 同步 Bundle 状态到 Meta,保证 meta 是唯一事实源
	b.Meta.Name = b.Name
	b.Meta.Domains = b.Domains
	b.Meta.NotBefore = b.NotBefore
	b.Meta.NotAfter = b.NotAfter
	b.Meta.Fingerprint = b.Fingerprint
	b.Meta.Issuer = b.Issuer
	b.Meta.IssuedAt = time.Now()

	files := []struct {
		name string
		data []byte
		perm os.FileMode
	}{
		{fileCert, b.LeafPEM, certPerm},
		{fileFullchain, b.FullchainPEM, certPerm},
		{fileChain, b.ChainPEM, certPerm},
		{filePrivkey, b.PrivateKeyPEM, keyPerm},
	}
	for _, fw := range files {
		if err := atomicWrite(filepath.Join(f.dir, fw.name), fw.data, fw.perm); err != nil {
			return fmt.Errorf("写入 %s: %w", fw.name, err)
		}
	}
	metaJSON, err := json.MarshalIndent(b.Meta, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 meta: %w", err)
	}
	if err := atomicWrite(filepath.Join(f.dir, fileMeta), metaJSON, metaPerm); err != nil {
		return fmt.Errorf("写入 meta.json: %w", err)
	}
	return nil
}

// Load 读取该目录下的证书包;未签发(无 cert.pem)返回 nil。
func (f *FS) Load(_ context.Context, name string) (*certmgr.Bundle, error) {
	b := &certmgr.Bundle{Name: name}
	var err error
	if b.LeafPEM, err = readFileIfExists(filepath.Join(f.dir, fileCert)); err != nil {
		return nil, err
	}
	if b.FullchainPEM, err = readFileIfExists(filepath.Join(f.dir, fileFullchain)); err != nil {
		return nil, err
	}
	if b.ChainPEM, err = readFileIfExists(filepath.Join(f.dir, fileChain)); err != nil {
		return nil, err
	}
	if b.PrivateKeyPEM, err = readFileIfExists(filepath.Join(f.dir, filePrivkey)); err != nil {
		return nil, err
	}
	if len(b.LeafPEM) == 0 {
		// 无证书:若存在 meta(如失败状态),返回带状态的 Bundle,否则视为未签发
		metaRaw, err := readFileIfExists(filepath.Join(f.dir, fileMeta))
		if err != nil {
			return nil, err
		}
		if len(metaRaw) > 0 {
			if err := json.Unmarshal(metaRaw, &b.Meta); err != nil {
				return nil, fmt.Errorf("解析 meta.json: %w", err)
			}
			if b.Meta.Name == "" {
				b.Meta.Name = name
			}
			b.Name = b.Meta.Name
			return b, nil
		}
		return nil, nil // 未签发
	}
	// 从 leaf 重建关键字段
	parsed, err := ParseLeaf(b.LeafPEM)
	if err != nil {
		return nil, err
	}
	b.Domains = parsed.DNSNames
	b.NotBefore = parsed.NotBefore
	b.NotAfter = parsed.NotAfter
	b.Issuer = parsed.Issuer
	b.Fingerprint = certmgr.FingerprintOf(b.LeafPEM)

	metaRaw, err := readFileIfExists(filepath.Join(f.dir, fileMeta))
	if err != nil {
		return nil, err
	}
	if len(metaRaw) > 0 {
		if err := json.Unmarshal(metaRaw, &b.Meta); err != nil {
			return nil, fmt.Errorf("解析 meta.json: %w", err)
		}
	}
	// name 以 meta 为事实源;meta 缺失时回退到参数
	if b.Meta.Name != "" {
		b.Name = b.Meta.Name
	} else {
		b.Meta.Name = name
		b.Name = name
	}
	return b, nil
}

// List 返回该目录下的托管条目(存在 meta.json 时 1 个,否则空)。
func (f *FS) List(ctx context.Context) ([]*certmgr.Bundle, error) {
	if _, err := os.Stat(filepath.Join(f.dir, fileMeta)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	b, err := f.Load(ctx, "")
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	out := []*certmgr.Bundle{b}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// UpdateMeta 仅更新 meta.json(证书文件保持不变),用于失败计数等状态变更。
func (f *FS) UpdateMeta(_ context.Context, meta *certmgr.Meta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 meta: %w", err)
	}
	return atomicWrite(filepath.Join(f.dir, fileMeta), data, metaPerm)
}

// --- 辅助 ---

func atomicWrite(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func readFileIfExists(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

// LeafInfo 是 leaf 证书的解析结果。
type LeafInfo struct {
	DNSNames  []string
	NotBefore time.Time
	NotAfter  time.Time
	Issuer    string
}

// ParseLeaf 解析 PEM 编码的 leaf 证书。
func ParseLeaf(leafPEM []byte) (*LeafInfo, error) {
	block, _ := pem.Decode(leafPEM)
	if block == nil {
		return nil, errors.New("非法的 PEM 数据")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析 X.509 证书: %w", err)
	}
	info := &LeafInfo{
		DNSNames:  append([]string{}, cert.DNSNames...),
		NotBefore: cert.NotBefore,
		NotAfter:  cert.NotAfter,
		Issuer:    cert.Issuer.CommonName,
	}
	if len(info.DNSNames) == 0 && cert.Subject.CommonName != "" {
		info.DNSNames = []string{cert.Subject.CommonName}
	}
	return info, nil
}
