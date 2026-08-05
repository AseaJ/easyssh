package acme

import (
	"fmt"
	"os"

	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/providers/dns/alidns"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/providers/dns/dnspod"
)

// dnsProviderFactory 按 lego provider 名构造 ChallengeProvider。
// lego 的 provider 从环境变量读取凭证;dnsOpts 会临时写入环境变量。
var dnsProviderFactory = map[string]func(opts map[string]string) (challenge.Provider, error){
	"cloudflare": func(opts map[string]string) (challenge.Provider, error) {
		applyOpts(opts)
		return cloudflare.NewDNSProvider()
	},
	"dnspod": func(opts map[string]string) (challenge.Provider, error) {
		applyOpts(opts)
		return dnspod.NewDNSProvider()
	},
	"alidns": func(opts map[string]string) (challenge.Provider, error) {
		applyOpts(opts)
		return alidns.NewDNSProvider()
	},
}

func newDNSProvider(name string, opts map[string]string) (challenge.Provider, error) {
	factory, ok := dnsProviderFactory[name]
	if !ok {
		return nil, fmt.Errorf("不支持的 dns_provider %q(当前支持: cloudflare/dnspod/alidns)", name)
	}
	provider, err := factory(opts)
	if err != nil {
		return nil, fmt.Errorf("初始化 dns provider %s: %w", name, err)
	}
	return provider, nil
}

// applyOpts 把配置里的 dns_provider_opts 写入环境变量(不覆盖已存在的),
// 满足 lego provider 从环境变量读凭证的约定。
func applyOpts(opts map[string]string) {
	for k, v := range opts {
		if _, ok := os.LookupEnv(k); !ok {
			os.Setenv(k, v)
		}
	}
}
