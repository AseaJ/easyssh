package bundle

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Bundle 是解包后的内存视图:清单 + 各段数据(未写入磁盘)。
type Bundle struct {
	Manifest *Manifest
	Config   []byte // config.yaml 原文
	Secrets  *SecretsPayload
	SSHKeys  *SSHKeysPayload
	Certs    map[string]map[string][]byte // 条目名 → 文件名 → 内容(与 storage 布局一致)
}

// Preview 是导入前展示给用户的摘要(不解密敏感数据)。
type Preview struct {
	Manifest    *Manifest
	CertNames   []string
	EnvSecrets  []string
	SSHKeyPaths []string
	HasSecrets  bool
	HasSSHKeys  bool
	HasCerts    bool
}

// ReadBundle 读取 .zsbundle 文件并解析清单与明文部分;敏感段按 scope 解密。
// 口令为空且包含加密段时返回 ErrNeedPassword。
var ErrNeedPassword = fmt.Errorf("该导出包含加密的密钥/私钥,需要提供口令")

// ReadBundle 打开包文件、校验清单、按需解密。
// 注意:本函数读取整个 zip 到内存,包大小受 zip 内条目上限保护(见 readZip)。
func ReadBundle(path, password string) (*Bundle, error) {
	entries, err := readZip(path)
	if err != nil {
		return nil, err
	}
	b := &Bundle{Certs: map[string]map[string][]byte{}}

	// 先解析 manifest(不依赖 zip 条目顺序)
	var m Manifest
	manifestFound := false
	for _, e := range entries {
		if e.name != entryManifest {
			continue
		}
		if err := json.Unmarshal(e.data, &m); err != nil {
			return nil, ErrFormat("解析清单失败: %v", err)
		}
		if err := m.validate(); err != nil {
			return nil, err
		}
		b.Manifest = &m
		manifestFound = true
		break
	}
	if !manifestFound {
		return nil, ErrFormat("包内缺少清单")
	}
	needPW := false

	for _, e := range entries {
		switch e.name {
		case entryManifest:
			// 已处理
		case entryConfig:
			b.Config = e.data
		case entrySecrets:
			if password == "" {
				needPW = true
				continue
			}
			plain, err := decryptBlob(password, e.data, b.kdf())
			if err != nil {
				return nil, err
			}
			var sp SecretsPayload
			if err := json.Unmarshal(plain, &sp); err != nil {
				return nil, ErrFormat("解析密钥段失败: %v", err)
			}
			b.Secrets = &sp
		case entrySSHKeys:
			if password == "" {
				needPW = true
				continue
			}
			plain, err := decryptBlob(password, e.data, b.kdf())
			if err != nil {
				return nil, err
			}
			var sp SSHKeysPayload
			if err := json.Unmarshal(plain, &sp); err != nil {
				return nil, ErrFormat("解析 SSH 密钥段失败: %v", err)
			}
			b.SSHKeys = &sp
		default:
			if len(e.name) > len(certsDir) && e.name[:len(certsDir)] == certsDir {
				rest := e.name[len(certsDir):]
				slash := bytes.IndexByte([]byte(rest), '/')
				if slash <= 0 || slash == len(rest)-1 {
					return nil, ErrFormat("非法证书条目路径 %q", e.name)
				}
				certName := rest[:slash]
				fileName := rest[slash+1:]
				if b.Certs[certName] == nil {
					b.Certs[certName] = map[string][]byte{}
				}
				b.Certs[certName][fileName] = e.data
			}
			// 未知顶层条目忽略(向前兼容)
		}
	}
	if needPW {
		return nil, ErrNeedPassword
	}
	if len(b.Config) == 0 {
		return nil, ErrFormat("包内缺少配置")
	}
	return b, nil
}

// kdf 返回清单中的 KDF 参数(含加密段时必有)。
func (b *Bundle) kdf() KDF { return b.Manifest.KDF }

// Preview 生成导入预览(清单 + 摘要,不涉及解密)。
func (b *Bundle) Preview() *Preview {
	m := b.Manifest
	p := &Preview{
		Manifest:   m,
		HasSecrets: m.Scope.Secrets,
		HasSSHKeys: m.Scope.SSHKeys,
		HasCerts:   m.Scope.Certs,
	}
	for _, c := range m.Certs {
		p.CertNames = append(p.CertNames, c.Name)
	}
	p.EnvSecrets = append([]string(nil), m.EnvSecrets...)
	for _, k := range m.SSHKeys {
		p.SSHKeyPaths = append(p.SSHKeyPaths, k.Path)
	}
	return p
}

// readZip 读取 zip 文件全部条目(内存);对条目数量与单条大小做基本限制,防 zip bomb。
func readZip(path string) ([]zipFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开导出包 %s: %w", path, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() > maxBundleSize {
		return nil, ErrFormat("导出包过大(%d 字节,上限 %d)", st.Size(), maxBundleSize)
	}
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		return nil, ErrFormat("不是有效的 zip 包: %v", err)
	}
	if len(zr.File) > maxZipEntries {
		return nil, ErrFormat("包内条目过多(%d,上限 %d)", len(zr.File), maxZipEntries)
	}
	var out []zipFile
	for _, zf := range zr.File {
		if zf.UncompressedSize64 > maxEntrySize {
			return nil, ErrFormat("条目 %s 过大(%d 字节)", zf.Name, zf.UncompressedSize64)
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, fmt.Errorf("读取条目 %s: %w", zf.Name, err)
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxEntrySize+1))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("读取条目 %s: %w", zf.Name, err)
		}
		out = append(out, zipFile{name: zf.Name, data: data})
	}
	return out, nil
}
