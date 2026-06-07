package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"quanty_trade/internal/conf"
	"strings"
)

func keyBytes() ([]byte, bool) {
	_ = conf.Load()
	raw := strings.TrimSpace(conf.C().Security.ConfigEncryptionKey)
	if raw == "" {
		return nil, false
	}
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil {
		if len(b) == 32 {
			return b, true
		}
	}
	if b, err := hex.DecodeString(raw); err == nil {
		if len(b) == 32 {
			return b, true
		}
	}
	if len(raw) == 32 {
		return []byte(raw), true
	}
	return nil, false
}

func EncryptString(plaintext string) (string, error) {
	key, ok := keyBytes()
	if !ok {
		// 关键修复：原来这里静默返回明文，意味着启动期没设
		// CONFIG_ENCRYPTION_KEY 时所有 API key/secret 都裸放进 DB。
		// 现在改为直接报错。启动期 conf.MustValidateSecurity() 已经
		// 强制要求该 key 存在，这条路径正常不应该到。
		return "", errors.New("missing CONFIG_ENCRYPTION_KEY: refusing to store secret in plaintext")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	out := append(nonce, ciphertext...)
	return "enc:" + base64.StdEncoding.EncodeToString(out), nil
}

func DecryptString(maybeCiphertext string) (string, error) {
	if !strings.HasPrefix(maybeCiphertext, "enc:") {
		return maybeCiphertext, nil
	}
	key, ok := keyBytes()
	if !ok {
		return "", errors.New("missing CONFIG_ENCRYPTION_KEY")
	}

	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(maybeCiphertext, "enc:"))
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("invalid ciphertext")
	}

	nonce := raw[:gcm.NonceSize()]
	ciphertext := raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
