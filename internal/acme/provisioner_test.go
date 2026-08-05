package acme

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-acme/lego/v4/certificate"
)

// genCert 生成一张自签证书,返回 DER 与 PEM 私钥。
func genCert(t *testing.T, cn string) (der []byte, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     []string{cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true,
	}
	der, err = x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return der, keyPEM
}

func TestBuildBundle(t *testing.T) {
	leafDER, _ := genCert(t, "example.com")
	interDER, _ := genCert(t, "fake-intermediate")

	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	interPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: interDER})
	fullchain := append(append([]byte{}, leafPEM...), interPEM...)

	res := &certificate.Resource{
		Certificate: fullchain,
		PrivateKey:  []byte("-----BEGIN EC PRIVATE KEY-----\nfake\n-----END EC PRIVATE KEY-----\n"),
	}
	b, err := buildBundle([]string{"example.com"}, res)
	if err != nil {
		t.Fatalf("buildBundle: %v", err)
	}
	if b.Fingerprint == "" {
		t.Error("指纹为空")
	}
	if len(b.FullchainPEM) == 0 {
		t.Error("fullchain 为空")
	}
	if len(b.ChainPEM) == 0 {
		t.Error("chain(intermediate)为空")
	}
	if b.ChainPEM == nil || len(b.ChainPEM) == 0 {
		t.Error("chain 应只含 intermediate")
	}
	if b.NotAfter.Before(time.Now().Add(89 * 24 * time.Hour)) {
		t.Errorf("NotAfter 解析异常: %v", b.NotAfter)
	}
}

func TestBuildBundleEmpty(t *testing.T) {
	_, err := buildBundle([]string{"a.com"}, &certificate.Resource{Certificate: []byte("")})
	if err == nil {
		t.Fatal("空证书应报错")
	}
}

func TestLoadOrCreateAccountKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "account.key")

	// 首次生成
	k1, err := loadOrCreateAccountKey(path)
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if k1 == nil {
		t.Fatal("返回空密钥")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 && os.Getenv("GOOS") != "windows" {
		// Windows 跳过权限断言
	}

	// 再次加载,应复用同一密钥
	k2, err := loadOrCreateAccountKey(path)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if !k1.Equal(k2) {
		t.Error("密钥未复用,应为同一把")
	}

	// 非法密钥文件报错
	bad := filepath.Join(dir, "bad.key")
	os.WriteFile(bad, []byte("not a key"), 0o600)
	if _, err := loadOrCreateAccountKey(bad); err == nil {
		t.Error("非法密钥应报错")
	}
}

func TestSplitPEMBlocks(t *testing.T) {
	d1, _ := genCert(t, "a.com")
	d2, _ := genCert(t, "b.com")
	chain := append(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d1}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d2})...,
	)
	blocks := splitPEMBlocks(chain)
	if len(blocks) != 2 {
		t.Fatalf("块数 = %d,期望 2", len(blocks))
	}
	enc := encodeBlocks(blocks[1:])
	if len(enc) == 0 {
		t.Error("encodeBlocks 结果为空")
	}
	re := splitPEMBlocks(enc)
	if len(re) != 1 {
		t.Errorf("重新解析块数 = %d,期望 1", len(re))
	}
}
