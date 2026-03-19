package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
)

func keyBytes() ([]byte, bool) {
	raw := strings.TrimSpace(os.Getenv("CONFIG_ENCRYPTION_KEY"))
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
		return plaintext, nil
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
