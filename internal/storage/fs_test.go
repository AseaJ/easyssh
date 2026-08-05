package storage

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/asea/easyssh/internal/certmgr"
)

// genSelfSigned 生成一张自签测试证书(PEM)。
func genSelfSigned(t *testing.T, domains []string, notAfter time.Time) (leafPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: domains[0]},
		DNSNames:     domains,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leafPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return leafPEM, keyPEM
}

// genSelfSignedNoTest 供 testBundle 使用(不依赖 *testing.T)。
func genSelfSignedNoTest(name string, domains []string, notAfter time.Time) ([]byte, []byte) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     domains,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	leaf := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return leaf, keyPEM
}

func testBundle(name string, domains []string, notAfter time.Time) *certmgr.Bundle {
	leaf, key := genSelfSignedNoTest(name, domains, notAfter)
	return &certmgr.Bundle{
		Name:          name,
		Domains:       domains,
		LeafPEM:       leaf,
		FullchainPEM:  append(append([]byte{}, leaf...), leaf...), // 测试:链=两份
		ChainPEM:      leaf,
		PrivateKeyPEM: key,
		NotAfter:      notAfter,
		Fingerprint:   certmgr.FingerprintOf(leaf),
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	f, err := NewFS(root)
	if err != nil {
		t.Fatal(err)
	}
	notAfter := time.Now().Add(60 * 24 * time.Hour)
	b := testBundle("example", []string{"example.com", "www.example.com"}, notAfter)
	if err := f.Save(context.Background(), b); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := f.Load(context.Background(), "example")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("Load 返回 nil")
	}
	if loaded.Name != "example" {
		t.Errorf("Name = %q", loaded.Name)
	}
	if diff := loaded.NotAfter.Sub(notAfter); diff > time.Minute || diff < -time.Minute {
		t.Errorf("NotAfter 偏差过大: %v vs %v", loaded.NotAfter, notAfter)
	}
	if loaded.Fingerprint != b.Fingerprint {
		t.Errorf("指纹不一致: %s vs %s", loaded.Fingerprint, b.Fingerprint)
	}
	if len(loaded.Domains) != 2 {
		t.Errorf("Domains = %v", loaded.Domains)
	}
	if loaded.Meta.IssuedAt.IsZero() {
		t.Error("meta.IssuedAt 未写入")
	}
}

func TestPrivkeyPerm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 无 POSIX 权限位,该机制在 Linux 上生效") // 目标平台为 Linux
	}
	root := t.TempDir()
	f, _ := NewFS(root)
	b := testBundle("p", []string{"p.example.com"}, time.Now().Add(time.Hour))
	if err := f.Save(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, filePrivkey))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != keyPerm {
		t.Errorf("privkey 权限 = %v,期望 %v", perm, keyPerm)
	}
	ci, _ := os.Stat(filepath.Join(root, fileCert))
	if perm := ci.Mode().Perm(); perm != certPerm {
		t.Errorf("cert 权限 = %v,期望 %v", perm, certPerm)
	}
}

func TestLoadNotIssued(t *testing.T) {
	root := t.TempDir()
	f, _ := NewFS(root)
	got, err := f.Load(context.Background(), "missing")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("未签发的条目应返回 nil")
	}
}

func TestListOnlyManaged(t *testing.T) {
	root := t.TempDir()
	f, _ := NewFS(root)
	f.Save(context.Background(), testBundle("a", []string{"a.example.com"}, time.Now().Add(time.Hour)))

	list, err := f.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("List 长度 = %d,期望 1", len(list))
	}
	if list[0].Name != "a" {
		t.Errorf("Name = %q", list[0].Name)
	}

	// 无 meta 的目录视为未托管
	empty := filepath.Join(t.TempDir(), "e")
	os.MkdirAll(empty, 0o700)
	fe, _ := NewFS(empty)
	le, err := fe.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(le) != 0 {
		t.Errorf("未托管目录 List 长度 = %d,期望 0", len(le))
	}
}

func TestOverwriteAtomic(t *testing.T) {
	root := t.TempDir()
	f, _ := NewFS(root)
	old := testBundle("x", []string{"x.example.com"}, time.Now().Add(10*24*time.Hour))
	f.Save(context.Background(), old)
	newB := testBundle("x", []string{"x.example.com"}, time.Now().Add(50*24*time.Hour))
	if err := f.Save(context.Background(), newB); err != nil {
		t.Fatal(err)
	}
	loaded, _ := f.Load(context.Background(), "x")
	if loaded.Fingerprint != newB.Fingerprint {
		t.Error("覆盖保存后指纹未更新")
	}
	if _, err := os.Stat(filepath.Join(root, "cert.pem.tmp")); !os.IsNotExist(err) {
		t.Error("临时文件残留")
	}
}
