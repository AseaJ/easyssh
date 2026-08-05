// Command go-zs 是证书自动化托管工具的 CLI 入口(headless 模式)。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"go-zs/internal/acme"
	"go-zs/internal/app"
	"go-zs/internal/config"
	"go-zs/internal/scheduler"
	"go-zs/internal/storage"
)

// version 是 CLI 版本号,构建时可通过 -ldflags "-X main.version=vX.Y.Z" 注入。
var version = "0.1.0-dev"

func main() {
	// Windows:从注册表加载先前持久化的环境变量(密钥),CLI 与 GUI 行为一致
	app.LoadPersistedEnv()
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "go-zs",
		Short:   "证书自动化托管工具",
		Version: version,
	}
	root.PersistentFlags().StringP("config", "c", "go-zs.yaml", "配置文件路径")
	root.AddCommand(newValidateConfigCmd())
	root.AddCommand(newListCmd())
	root.AddCommand(newInspectCmd())
	root.AddCommand(newIssueCmd())
	root.AddCommand(newServeCmd())
	root.AddCommand(newRenewCmd())
	root.AddCommand(newExportCmd())
	root.AddCommand(newImportCmd())
	return root
}

func loadCfg(cmd *cobra.Command) (*config.Config, error) {
	path, _ := cmd.Flags().GetString("config")
	cfg, err := config.LoadAndExpand(path)
	if err != nil {
		return nil, err
	}
	// 本地相对路径基于配置文件目录解析为绝对,保证从任意工作目录运行都能定位证书/密钥
	config.ResolvePaths(cfg, filepath.Dir(path))
	return cfg, nil
}

func newValidateConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate-config",
		Short: "校验配置文件(含 {{env:VAR}} 展开)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg(cmd)
			if err != nil {
				return err
			}
			fmt.Printf("配置校验通过\n")
			fmt.Printf("  CA server : %s\n", cfg.CA.Server)
			fmt.Printf("  CA email  : %s\n", cfg.CA.Email)
			fmt.Printf("  证书条目   : %d 个\n", len(cfg.Certificates))
			for _, c := range cfg.Certificates {
				fmt.Printf("    - %-12s %v  [%s] dir=%s\n", c.Name, c.Domains, c.Challenge, c.Storage.Dir)
			}
			fmt.Printf("  调度       : 每 %s 扫描,剩余 %s 触发续期\n",
				cfg.Schedule.CheckInterval.Std(), cfg.Schedule.RenewBefore.Std())
			return nil
		},
	}
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "列出全部托管证书与剩余天数",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg(cmd)
			if err != nil {
				return err
			}
			type row struct {
				name, domains, remain, fp, status string
				notAfter                           time.Time
			}
			var rows []row
			for _, c := range cfg.Certificates {
				f, err := storage.NewFS(c.Storage.Dir)
				if err != nil {
					return err
				}
				b, err := f.Load(context.Background(), c.Name)
				if err != nil {
					return fmt.Errorf("读取 %s: %w", c.Name, err)
				}
				if b == nil {
					rows = append(rows, row{c.Name, strings.Join(c.Domains, ","), "-", "-", "未签发", time.Time{}})
					continue
				}
				remain := time.Until(b.NotAfter)
				days := int(remain.Hours() / 24)
				status := "fresh"
				if b.Meta.FailCount > 0 {
					status = "retry"
				}
				if remain <= 0 {
					status = "expired"
				} else if b.ExpiringIn(cfg.Schedule.RenewBefore.Std()) {
					status = "renewing"
				}
				rows = append(rows, row{
					c.Name, strings.Join(c.Domains, ","),
					fmt.Sprintf("%dd", days), shortFP(b.Fingerprint), status, b.NotAfter,
				})
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
			fmt.Printf("%-16s %-42s %-8s %-10s %-12s %s\n", "名称", "域名", "剩余", "状态", "指纹(短)", "到期时间")
			for _, r := range rows {
				na := "-"
				if !r.notAfter.IsZero() {
					na = r.notAfter.Format("2006-01-02 15:04")
				}
				fmt.Printf("%-16s %-42s %-8s %-10s %-12s %s\n", r.name, r.domains, r.remain, r.status, r.fp, na)
			}
			return nil
		},
	}
}

func newInspectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <name>",
		Short: "查看指定证书条目的详细信息",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg(cmd)
			if err != nil {
				return err
			}
			var entry *config.CertificateConfig
			for i := range cfg.Certificates {
				if cfg.Certificates[i].Name == args[0] {
					entry = &cfg.Certificates[i]
					break
				}
			}
			if entry == nil {
				return fmt.Errorf("配置中不存在条目 %q", args[0])
			}
			f, err := storage.NewFS(entry.Storage.Dir)
			if err != nil {
				return err
			}
			b, err := f.Load(context.Background(), entry.Name)
			if err != nil {
				return err
			}
			fmt.Printf("条目     : %s\n", entry.Name)
			fmt.Printf("域名     : %v\n", entry.Domains)
			fmt.Printf("挑战     : %s", entry.Challenge)
			if entry.Challenge == "dns-01" {
				fmt.Printf(" (provider: %s)", entry.DNSProvider)
			}
			fmt.Println()
			fmt.Printf("存储目录 : %s\n", entry.Storage.Dir)
			if b == nil {
				fmt.Println("状态     : 未签发")
				return nil
			}
			fmt.Printf("状态     : 已签发\n")
			fmt.Printf("有效期   : %s ~ %s\n", b.NotBefore.Format("2006-01-02 15:04"), b.NotAfter.Format("2006-01-02 15:04"))
			fmt.Printf("剩余     : %s\n", time.Until(b.NotAfter).Round(time.Minute))
			fmt.Printf("SAN      : %v\n", b.Domains)
			fmt.Printf("指纹     : %s\n", b.Fingerprint)
			fmt.Printf("签发者   : %s\n", b.Issuer)
			fmt.Printf("本地签发 : %s\n", b.Meta.IssuedAt.Format("2006-01-02 15:04"))
			fmt.Printf("失败次数 : %d\n", b.Meta.FailCount)
			if b.Meta.LastError != "" {
				fmt.Printf("最近错误 : %s\n", b.Meta.LastError)
			}
			return nil
		},
	}
}

func newIssueCmd() *cobra.Command {
	var httpPort string
	cmd := &cobra.Command{
		Use:   "issue <name>",
		Short: "立即签发/续期指定条目(默认走配置的 CA,通常为 staging)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg(cmd)
			if err != nil {
				return err
			}
			var entry *config.CertificateConfig
			for i := range cfg.Certificates {
				if cfg.Certificates[i].Name == args[0] {
					entry = &cfg.Certificates[i]
					break
				}
			}
			if entry == nil {
				return fmt.Errorf("配置中不存在条目 %q", args[0])
			}
			fmt.Printf("签发条目 %q: 域名 %v,挑战 %s\n", entry.Name, entry.Domains, entry.Challenge)
			if entry.Challenge == "dns-01" {
				fmt.Printf("  dns provider: %s\n", entry.DNSProvider)
			}

			prov, err := acme.New(acme.Options{
				Server:      cfg.CA.Server,
				Email:       cfg.CA.Email,
				AccountKey:  cfg.CA.AccountKey,
				Challenge:   entry.Challenge,
				DNSProvider: entry.DNSProvider,
				DNSOpts:     entry.DNSProviderOpts,
				HTTPPort:    httpPort,
			})
			if err != nil {
				return err
			}

			bundle, err := prov.Ensure(context.Background(), entry.Domains)
			if err != nil {
				return err
			}

			store, err := storage.NewFS(entry.Storage.Dir)
			if err != nil {
				return err
			}
			if err := store.Save(context.Background(), bundle); err != nil {
				return fmt.Errorf("保存证书: %w", err)
			}
			fmt.Printf("签发成功:\n")
			fmt.Printf("  有效期   : %s ~ %s\n", bundle.NotBefore.Format("2006-01-02 15:04"), bundle.NotAfter.Format("2006-01-02 15:04"))
			fmt.Printf("  剩余     : %s\n", time.Until(bundle.NotAfter).Round(time.Minute))
			fmt.Printf("  指纹     : %s\n", bundle.Fingerprint)
			fmt.Printf("  签发者   : %s\n", bundle.Issuer)
			fmt.Printf("  存储目录 : %s\n", entry.Storage.Dir)
			return nil
		},
	}
	cmd.Flags().StringVar(&httpPort, "http-port", "80", "http-01 挑战监听端口")
	return cmd
}

func newServeCmd() *cobra.Command {
	var pidFile string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "以守护进程方式常驻运行(周期扫描/续期/部署)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg(cmd)
			if err != nil {
				return err
			}
			logger := log.New(os.Stdout, "", log.LstdFlags)
			sched := scheduler.New(cfg, nil, logger)

			release, err := app.AcquirePIDFile(pidFile)
			if err != nil {
				return err
			}
			defer release()

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			logger.Printf("go-zs serve 启动,调度配置: %s", sched.Describe())
			interval := cfg.Schedule.CheckInterval.Std()
			for {
				reports := sched.RunOnce(ctx)
				for _, r := range reports {
					status := "OK"
					if !r.OK {
						status = "FAIL"
					}
					logger.Printf("[%s] %s %s %s", r.Name, status, r.Action, r.Error)
				}
				select {
				case <-ctx.Done():
					logger.Printf("收到退出信号,优雅退出")
					return nil
				case <-time.After(interval):
				}
			}
		},
	}
	cmd.Flags().StringVar(&pidFile, "pidfile", "go-zs.pid", "PID 文件路径(单实例锁);为空则禁用")
	return cmd
}

func newRenewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "renew <name>",
		Short: "强制重新签发并部署指定条目(忽略到期判断)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg(cmd)
			if err != nil {
				return err
			}
			sched := scheduler.New(cfg, nil, log.New(os.Stderr, "", 0))
			rep, err := sched.ForceRenew(context.Background(), args[0])
			if err != nil {
				return err
			}
			if !rep.OK {
				return fmt.Errorf("%s", rep.Error)
			}
			fmt.Printf("续期成功: %s\n", args[0])
			return nil
		},
	}
	return cmd
}

func shortFP(fp string) string {
	if len(fp) > 12 {
		return fp[:12]
	}
	return fp
}
