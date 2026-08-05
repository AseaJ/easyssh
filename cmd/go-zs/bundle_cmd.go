package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"go-zs/internal/app"
	"go-zs/internal/bundle"
)

// newExportCmd 导出证书配置/密钥/产物为 .zsbundle 包。
//
//	go-zs export -c go-zs.yaml -o out.zsbundle --scope config,secrets,certs,ssh-keys
//
// scope 含 secrets 或 ssh-keys 时必须提供口令(--password 或交互输入)。
func newExportCmd() *cobra.Command {
	var (
		out     string
		scope   string
		passwd  string
		useTerm bool
	)
	cmd := &cobra.Command{
		Use:   "export",
		Short: "导出证书配置/密钥/产物为 .zsbundle 包(可分档:config / secrets / certs / ssh-keys)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			s, err := parseScope(scope)
			if err != nil {
				return err
			}
			if s.NeedsPassword() && passwd == "" && !useTerm {
				useTerm = true // 含敏感段但未给口令:交互输入
			}
			if useTerm && !s.NeedsPassword() {
				return fmt.Errorf("仅配置导出不需要口令,--password 或 --stdin-password 多余")
			}
			if useTerm {
				if !term.IsTerminal(int(os.Stdin.Fd())) {
					return fmt.Errorf("无法交互输入口令(非终端),请用 --password 或设置环境变量 GOZS_EXPORT_PASSWORD")
				}
				fmt.Fprint(os.Stderr, "请输入导出口令(至少 10 字符,非纯数字): ")
				p, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Fprintln(os.Stderr)
				if err != nil {
					return fmt.Errorf("读取口令失败: %w", err)
				}
				passwd = string(p)
			}
			m, err := bundle.Export(out, bundle.ExportOptions{
				ConfigPath: cfgPath,
				Scope:      s,
				Password:   passwd,
			})
			if err != nil {
				return err
			}
			fmt.Printf("已导出到 %s\n", out)
			fmt.Printf("  范围      : config=%v secrets=%v certs=%v ssh_keys=%v\n",
				m.Scope.Config, m.Scope.Secrets, m.Scope.Certs, m.Scope.SSHKeys)
			if len(m.Certs) > 0 {
				fmt.Printf("  证书条目   : %s\n", certNames(m))
			}
			if len(m.EnvSecrets) > 0 {
				fmt.Printf("  密钥变量   : %s\n", strings.Join(m.EnvSecrets, ", "))
			}
			if len(m.SSHKeys) > 0 {
				fmt.Printf("  SSH 私钥   : %d 个\n", len(m.SSHKeys))
			}
			fmt.Printf("  警告      : %s\n", m.Warning)
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "导出包路径(默认 <config 同目录>/go-zs-<时间戳>.zsbundle)")
	cmd.Flags().StringVar(&scope, "scope", "config", "导出范围(逗号分隔):config,secrets,certs,ssh-keys")
	cmd.Flags().StringVar(&passwd, "password", "", "导出口令(含 secrets/ssh-keys 时必填;也可交互输入)")
	cmd.Flags().BoolVar(&useTerm, "stdin-password", false, "强制交互输入口令")
	return cmd
}

// newImportCmd 导入 .zsbundle 包到当前配置。
//
//	go-zs import -c go-zs.yaml -f out.zsbundle --conflict append
func newImportCmd() *cobra.Command {
	var (
		file     string
		passwd   string
		conflict string
		yes      bool
	)
	cmd := &cobra.Command{
		Use:   "import",
		Short: "导入 .zsbundle 包(合并进当前配置,可处理冲突)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			if file == "" {
				return fmt.Errorf("请用 -f 指定 .zsbundle 包路径")
			}
			mode, err := bundle.ParseConflict(conflict)
			if err != nil {
				return err
			}
			// 先无口令读取:预览清单(含加密段则需口令)
			b, err := bundle.ReadBundle(file, "")
			if err != nil {
				if err != bundle.ErrNeedPassword {
					return err
				}
				if passwd == "" {
					if !term.IsTerminal(int(os.Stdin.Fd())) {
						return fmt.Errorf("该包含加密的密钥/私钥,需要口令;请用 --password 或交互输入")
					}
					fmt.Fprint(os.Stderr, "请输入导入口令: ")
					p, err2 := term.ReadPassword(int(os.Stdin.Fd()))
					fmt.Fprintln(os.Stderr)
					if err2 != nil {
						return fmt.Errorf("读取口令失败: %w", err2)
					}
					passwd = string(p)
				}
				b, err = bundle.ReadBundle(file, passwd)
				if err != nil {
					return err
				}
			}
			// 预览
			pv := b.Preview()
			fmt.Printf("导出包内容预览:\n")
			fmt.Printf("  导出时间   : %s\n", pv.Manifest.ExportedAt)
			fmt.Printf("  范围       : config=%v secrets=%v certs=%v ssh_keys=%v\n",
				pv.Manifest.Scope.Config, pv.Manifest.Scope.Secrets, pv.Manifest.Scope.Certs, pv.Manifest.Scope.SSHKeys)
			if len(pv.CertNames) > 0 {
				fmt.Printf("  证书条目   : %s\n", strings.Join(pv.CertNames, ", "))
			}
			if len(pv.EnvSecrets) > 0 {
				fmt.Printf("  密钥变量   : %s\n", strings.Join(pv.EnvSecrets, ", "))
			}
			if len(pv.SSHKeyPaths) > 0 {
				fmt.Printf("  SSH 私钥   : %s\n", strings.Join(pv.SSHKeyPaths, ", "))
			}
			if !yes {
				fmt.Printf("确认导入到 %s ?(冲突策略: %s)[y/N] ", cfgPath, mode)
				var ans string
				if _, err := fmt.Scanln(&ans); err != nil || !strings.EqualFold(ans, "y") {
					fmt.Println("已取消")
					return nil
				}
			}
			res, err := bundle.ApplyImport(b, bundle.MergeOptions{
				ConfigPath: cfgPath,
				Password:   passwd,
				Conflict:   mode,
				PersistEnv: app.PersistEnv,
			})
			if err != nil {
				return err
			}
			fmt.Printf("导入完成:\n")
			if len(res.ImportedCerts) > 0 {
				fmt.Printf("  导入条目   : %s\n", strings.Join(res.ImportedCerts, ", "))
			}
			if len(res.ImportedHosts) > 0 {
				fmt.Printf("  导入 hosts : %s\n", strings.Join(res.ImportedHosts, ", "))
			}
			if len(res.Renamed) > 0 {
				fmt.Printf("  冲突改名   : %s\n", strings.Join(res.Renamed, ", "))
			}
			if len(res.Skipped) > 0 {
				fmt.Printf("  已跳过     : %s\n", strings.Join(res.Skipped, ", "))
			}
			if len(res.EnvSecrets) > 0 {
				fmt.Printf("  密钥已写入 : %s\n", strings.Join(res.EnvSecrets, ", "))
			}
			if len(res.SSHKeys) > 0 {
				fmt.Printf("  SSH 私钥已写入 : %s\n", strings.Join(res.SSHKeys, ", "))
			}
			for _, n := range res.Notes {
				fmt.Printf("  提示       : %s\n", n)
			}
			if len(res.MissingEnv) > 0 {
				fmt.Printf("  注意:以下环境变量未设置(导入后需补齐): %s\n", strings.Join(res.MissingEnv, ", "))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", ".zsbundle 包路径(必填)")
	cmd.Flags().StringVar(&passwd, "password", "", "导入口令(含加密段时必填)")
	cmd.Flags().StringVar(&conflict, "conflict", "append", "冲突策略: append/skip/overwrite")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "跳过确认直接导入")
	return cmd
}

// parseScope 解析 "config,secrets,certs,ssh-keys" 为 Scope。
func parseScope(s string) (bundle.Scope, error) {
	var sc bundle.Scope
	for _, part := range strings.Split(s, ",") {
		switch strings.TrimSpace(strings.ToLower(part)) {
		case "":
		case "config":
			sc.Config = true
		case "secrets":
			sc.Secrets = true
		case "certs":
			sc.Certs = true
		case "ssh-keys", "sshkeys":
			sc.SSHKeys = true
		default:
			return sc, fmt.Errorf("未知导出范围 %q(可选: config/secrets/certs/ssh-keys)", part)
		}
	}
	if !sc.Config {
		sc.Config = true // config 恒为底座
	}
	return sc, nil
}

func certNames(m *bundle.Manifest) string {
	var names []string
	for _, c := range m.Certs {
		names = append(names, c.Name)
	}
	return strings.Join(names, ", ")
}
