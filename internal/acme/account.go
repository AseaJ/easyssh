package acme

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// loadOrCreateAccountKey 加载账号密钥;不存在则生成 ECDSA P-256 并保存(0600)。
func loadOrCreateAccountKey(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		key, perr := parseECKey(data)
		if perr != nil {
			return nil, fmt.Errorf("解析账号密钥 %s: %w", path, perr)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("读取账号密钥 %s: %w", path, err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("生成账号密钥: %w", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		return nil, fmt.Errorf("保存账号密钥 %s: %w", path, err)
	}
	return key, nil
}

func parseECKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("非法的 PEM 数据")
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	// 兼容 PKCS8
	anyKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("不是 EC 私钥(PEM 或 PKCS8): %w", err)
	}
	key, ok := anyKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("密钥类型不是 ECDSA")
	}
	return key, nil
}
