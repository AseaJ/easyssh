// Package acme 定义签发源(Provisioner)抽象,并基于 go-acme/lego 提供 ACME 实现。
package acme

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/http01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"

	"go-zs/internal/certmgr"
	"go-zs/internal/storage"
)

// Options 是 ACME Provisioner 的构造参数(来自配置)。
type Options struct {
	Server      string
	Email       string
	AccountKey  string
	Challenge   string // http-01 | dns-01
	DNSProvider string // dns-01 时的服务商(lego provider 名)
	DNSOpts     map[string]string
	HTTPPort    string // http-01 监听端口,默认 80
}

// Provisioner 是签发源抽象。
type Provisioner interface {
	// Name 返回签发源类型标识。
	Name() string
	// Ensure 确保为 domains 提供有效证书:无证书则签发,有则续期(换新私钥)。
	Ensure(ctx context.Context, domains []string) (*certmgr.Bundle, error)
}

type provisioner struct {
	opts   Options
	client *lego.Client
}

// New 创建 ACME Provisioner:加载/生成账号密钥、连接 CA、绑定挑战方式。
func New(opts Options) (*provisioner, error) {
	key, err := loadOrCreateAccountKey(opts.AccountKey)
	if err != nil {
		return nil, err
	}
	user := &acmeUser{email: opts.Email, key: key}

	config := lego.NewConfig(user)
	config.CADirURL = opts.Server // 使用配置的 CA(默认 staging)
	config.Certificate.KeyType = certcrypto.EC256

	client, err := lego.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("创建 ACME 客户端: %w", err)
	}

	switch opts.Challenge {
	case "http-01":
		port := opts.HTTPPort
		if port == "" {
			port = "80"
		}
		// lego 在挑战期间自动启停本地 HTTP server
		if err := client.Challenge.SetHTTP01Provider(http01.NewProviderServer("", port)); err != nil {
			return nil, fmt.Errorf("配置 http-01: %w", err)
		}
	case "dns-01":
		provider, err := newDNSProvider(opts.DNSProvider, opts.DNSOpts)
		if err != nil {
			return nil, err
		}
		if err := client.Challenge.SetDNS01Provider(provider); err != nil {
			return nil, fmt.Errorf("配置 dns-01: %w", err)
		}
	default:
		return nil, fmt.Errorf("未知挑战方式 %q", opts.Challenge)
	}

	return &provisioner{opts: opts, client: client}, nil
}

func (p *provisioner) Name() string { return "acme" }

// Ensure 走完整 ACME 流程:注册账号(幂等)→ 签发 → 组装 Bundle。
func (p *provisioner) Ensure(ctx context.Context, domains []string) (*certmgr.Bundle, error) {
	// 注册/确认账号(对已存在账号幂等)
	if _, err := p.client.Registration.Register(registration.RegisterOptions{
		TermsOfServiceAgreed: true,
	}); err != nil {
		return nil, fmt.Errorf("ACME 账号注册失败: %w", err)
	}

	req := certificate.ObtainRequest{
		Domains: domains,
		Bundle:  true, // 返回完整链(leaf + intermediates)
	}
	res, err := p.client.Certificate.Obtain(req)
	if err != nil {
		return nil, fmt.Errorf("ACME 签发失败: %w", err)
	}
	return buildBundle(domains, res)
}

// --- ACME 用户 ---

type acmeUser struct {
	email string
	key   *ecdsa.PrivateKey
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return nil }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

// --- Bundle 组装 ---

func buildBundle(domains []string, res *certificate.Resource) (*certmgr.Bundle, error) {
	blocks := splitPEMBlocks(res.Certificate)
	if len(blocks) == 0 {
		return nil, errors.New("ACME 返回的证书为空")
	}
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: blocks[0]})
	chainPEM := encodeBlocks(blocks[1:])

	info, err := storage.ParseLeaf(leafPEM)
	if err != nil {
		return nil, fmt.Errorf("解析 leaf 证书: %w", err)
	}

	return &certmgr.Bundle{
		Domains:       info.DNSNames,
		LeafPEM:       leafPEM,
		FullchainPEM:  res.Certificate,
		ChainPEM:      chainPEM,
		PrivateKeyPEM: res.PrivateKey,
		NotBefore:     info.NotBefore,
		NotAfter:      info.NotAfter,
		Fingerprint:   certmgr.FingerprintOf(leafPEM),
		Issuer:        info.Issuer,
	}, nil
}

// splitPEMBlocks 把 PEM 链拆成 DER 块列表。
func splitPEMBlocks(data []byte) [][]byte {
	var blocks [][]byte
	rest := data
	for {
		block, tail := pem.Decode(rest)
		if block == nil {
			break
		}
		blocks = append(blocks, block.Bytes)
		rest = tail
	}
	return blocks
}

func encodeBlocks(ders [][]byte) []byte {
	var sb strings.Builder
	for _, der := range ders {
		sb.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	}
	return []byte(sb.String())
}
