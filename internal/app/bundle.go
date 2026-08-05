package app

import (
	"fmt"
	"path/filepath"

	"github.com/asea/easyssh/internal/bundle"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// SelectSavePath 弹出系统「另存为」对话框,返回用户选择的完整保存路径。
// 用户取消时返回空字符串(无错误)。
// 注意:Wails v2 绑定方法不能注入 context.Context,须用 App.Startup 保存的 a.ctx。
func (a *App) SelectSavePath(opts runtime.SaveDialogOptions) (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("应用尚未就绪,无法弹出对话框")
	}
	return runtime.SaveFileDialog(a.ctx, opts)
}

// SelectOpenPath 弹出系统「打开文件」对话框,返回用户选择的文件路径。
// 用户取消时返回空字符串(无错误)。
func (a *App) SelectOpenPath(opts runtime.OpenDialogOptions) (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("应用尚未就绪,无法弹出对话框")
	}
	return runtime.OpenFileDialog(a.ctx, opts)
}

// ExportRequest 是 GUI 导出请求(Wails 前端传参)。
type ExportRequest struct {
	OutPath  string         `json:"out_path"`  // 导出包路径(必填)
	Scope    bundle.Scope   `json:"scope"`     // 导出范围
	Password string         `json:"password"`  // 口令(含 secrets/ssh-keys 时必填)
}

// ExportResult 是导出结果摘要。
type ExportResult struct {
	OutPath    string   `json:"out_path"`
	CertNames  []string `json:"cert_names"`
	EnvSecrets []string `json:"env_secrets"`
	SSHKeyN    int      `json:"ssh_key_n"`
	Warning    string   `json:"warning"`
}

// ExportBundle 导出配置/密钥/产物为 .zsbundle 包(GUI 证书列表页「导出」按钮)。
func (a *App) ExportBundle(req ExportRequest) (*ExportResult, error) {
	if req.OutPath == "" {
		return nil, fmt.Errorf("请选择导出包保存路径")
	}
	if req.Scope.Config == false {
		req.Scope.Config = true
	}
	if req.Scope.NeedsPassword() && req.Password == "" {
		return nil, fmt.Errorf("含密钥/私钥的导出需要设置口令")
	}
	// 默认导出路径:配置目录下 easyssh-<name>.zsbundle
	m, err := bundle.Export(req.OutPath, bundle.ExportOptions{
		ConfigPath: a.cfgPath(),
		Scope:      req.Scope,
		Password:   req.Password,
	})
	if err != nil {
		return nil, err
	}
	res := &ExportResult{
		OutPath:    req.OutPath,
		EnvSecrets: m.EnvSecrets,
		SSHKeyN:    len(m.SSHKeys),
		Warning:    m.Warning,
	}
	for _, c := range m.Certs {
		res.CertNames = append(res.CertNames, c.Name)
	}
	a.logger.Printf("已导出 bundle 到 %s(scope=%+v)", req.OutPath, req.Scope)
	return res, nil
}

// ImportPreviewRequest 是导入预览请求。
type ImportPreviewRequest struct {
	FilePath string `json:"file_path"`
	Password string `json:"password"`
}

// ImportPreview 是导入前展示给用户的摘要。
type ImportPreview struct {
	ExportedAt  string   `json:"exported_at"`
	HasSecrets  bool     `json:"has_secrets"`
	HasSSHKeys  bool     `json:"has_ssh_keys"`
	HasCerts    bool     `json:"has_certs"`
	CertNames   []string `json:"cert_names"`
	EnvSecrets  []string `json:"env_secrets"`
	SSHKeyPaths []string `json:"ssh_key_paths"`
	NeedsPass   bool     `json:"needs_pass"`
	Warning     string   `json:"warning"`
}

// PreviewImportBundle 预览导入包内容(不落盘)。
func (a *App) PreviewImportBundle(req ImportPreviewRequest) (*ImportPreview, error) {
	b, err := bundle.ReadBundle(req.FilePath, req.Password)
	if err != nil {
		if err == bundle.ErrNeedPassword {
			return &ImportPreview{NeedsPass: true}, nil
		}
		return nil, err
	}
	m := b.Manifest
	pv := b.Preview()
	return &ImportPreview{
		ExportedAt:  m.ExportedAt,
		HasSecrets:  pv.HasSecrets,
		HasSSHKeys:  pv.HasSSHKeys,
		HasCerts:    pv.HasCerts,
		CertNames:   pv.CertNames,
		EnvSecrets:  pv.EnvSecrets,
		SSHKeyPaths: pv.SSHKeyPaths,
		Warning:     m.Warning,
	}, nil
}

// ImportRequest 是导入请求。
type ImportRequest struct {
	FilePath string             `json:"file_path"`
	Password string             `json:"password"`
	Conflict bundle.ConflictMode `json:"conflict"` // append/skip/overwrite
}

// ImportBundle 导入 .zsbundle 包到当前配置并落盘。
func (a *App) ImportBundle(req ImportRequest) (string, error) {
	if req.FilePath == "" {
		return "", fmt.Errorf("请选择导出包文件")
	}
	if req.Conflict == "" {
		req.Conflict = bundle.ConflictAppend
	}
	b, err := bundle.ReadBundle(req.FilePath, req.Password)
	if err != nil {
		return "", err
	}
	res, err := bundle.ApplyImport(b, bundle.MergeOptions{
		ConfigPath: a.cfgPath(),
		Password:   req.Password,
		Conflict:   req.Conflict,
		TargetBase: filepath.Dir(a.cfgPath()),
		PersistEnv: PersistEnv,
	})
	if err != nil {
		return "", err
	}
	// 重新加载配置使 GUI 状态同步
	if err := a.reload(); err != nil {
		return "", fmt.Errorf("导入完成但重载失败: %w", err)
	}
	msg := fmt.Sprintf("导入完成:%d 个证书条目", len(res.ImportedCerts))
	if len(res.Renamed) > 0 {
		msg += ";冲突改名:" + joinStrings(res.Renamed)
	}
	if len(res.Skipped) > 0 {
		msg += ";跳过:" + joinStrings(res.Skipped)
	}
	if len(res.MissingEnv) > 0 {
		msg += ";待补齐环境变量:" + joinStrings(res.MissingEnv)
	}
	for _, n := range res.Notes {
		a.logger.Printf("[import] %s", n)
	}
	a.logger.Printf("导入 bundle 完成: %s", msg)
	return msg, nil
}

func joinStrings(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}
