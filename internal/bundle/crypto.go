package bundle

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/crypto/scrypt"
)

// 加密文件二进制头(magic 防误识别,version 预留算法演进)。
const (
	cryptMagic   = "GZSB"
	cryptVersion = byte(1)
	saltLen      = 16
	nonceLen     = 12
)

// ErrBadPassword 表示口令错误或密文损坏(两者在解密端无法区分)。
var ErrBadPassword = errors.New("口令错误或文件已损坏")

// ErrFormat 表示包结构/格式非法。
func ErrFormat(format string, args ...interface{}) error {
	return fmt.Errorf("bundle 格式错误: "+format, args...)
}

// deriveKey 用口令与盐派生 AES-256 密钥。
func deriveKey(password string, salt []byte, kdf KDF) ([]byte, error) {
	if kdf.Algo != "scrypt" {
		return nil, ErrFormat("未知 KDF 算法 %q", kdf.Algo)
	}
	key, err := scrypt.Key([]byte(password), salt, kdf.N, kdf.R, kdf.P, kdf.KeyLen)
	if err != nil {
		return nil, fmt.Errorf("派生密钥失败: %w", err)
	}
	return key, nil
}

// encryptBlob 用口令加密明文,输出:
//
//	"GZSB" | version(1B) | salt(16B) | nonce(12B) | AES-GCM 密文+tag
//
// 每次调用生成独立 salt 与 nonce,同一口令下不同文件不共享密钥材料。
func encryptBlob(password string, plaintext []byte, kdf KDF) ([]byte, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("生成盐: %w", err)
	}
	key, err := deriveKey(password, salt, kdf)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("生成 nonce: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, nonce, plaintext, nil)

	out := make([]byte, 0, len(cryptMagic)+1+saltLen+nonceLen+len(sealed))
	out = append(out, cryptMagic...)
	out = append(out, cryptVersion)
	out = append(out, salt...)
	out = append(out, nonce...)
	out = append(out, sealed...)
	return out, nil
}

// decryptBlob 解密 encryptBlob 的产物;口令错误或密文被篡改返回 ErrBadPassword。
func decryptBlob(password string, blob []byte, kdf KDF) ([]byte, error) {
	headLen := len(cryptMagic) + 1 + saltLen + nonceLen
	if len(blob) < headLen+16 {
		return nil, ErrBadPassword // 太短不可能是合法密文
	}
	if string(blob[:len(cryptMagic)]) != cryptMagic {
		return nil, ErrFormat("加密数据头不匹配(可能不是 go-zs 导出包)")
	}
	if blob[len(cryptMagic)] != cryptVersion {
		return nil, ErrFormat("不支持的加密版本 %d", blob[len(cryptMagic)])
	}
	salt := blob[len(cryptMagic)+1 : len(cryptMagic)+1+saltLen]
	nonce := blob[len(cryptMagic)+1+saltLen : headLen]
	sealed := blob[headLen:]

	key, err := deriveKey(password, salt, kdf)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, ErrBadPassword
	}
	return plain, nil
}

// validatePassword 检查口令强度:至少 10 字符且非纯数字。
func validatePassword(pw string) error {
	if len(pw) < 10 {
		return errors.New("口令至少 10 个字符")
	}
	allDigit := true
	for _, r := range pw {
		if r < '0' || r > '9' {
			allDigit = false
			break
		}
	}
	if allDigit {
		return errors.New("口令不能为纯数字")
	}
	return nil
}

// uint32ToBytes 辅助:长度前缀写入。
func uint32ToBytes(n uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, n)
	return b
}
