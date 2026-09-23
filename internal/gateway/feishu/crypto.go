package feishu

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

func decryptFeishuEvent(encryptKey, encrypted string) ([]byte, error) {
	if strings.TrimSpace(encryptKey) == "" {
		return nil, fmt.Errorf("encrypt_key is required")
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encrypted))
	if err != nil {
		return nil, fmt.Errorf("decode encrypted event: %w", err)
	}
	if len(data) < aes.BlockSize*2 || (len(data)-aes.BlockSize)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("encrypted event has an invalid length")
	}
	key := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("create event cipher: %w", err)
	}
	plain := make([]byte, len(data)-aes.BlockSize)
	cipher.NewCBCDecrypter(block, data[:aes.BlockSize]).CryptBlocks(plain, data[aes.BlockSize:])
	padding := int(plain[len(plain)-1])
	if padding < 1 || padding > aes.BlockSize || padding > len(plain) {
		return nil, fmt.Errorf("encrypted event has invalid padding")
	}
	var invalid byte
	for _, b := range plain[len(plain)-padding:] {
		invalid |= b ^ byte(padding)
	}
	if invalid != 0 {
		return nil, fmt.Errorf("encrypted event has invalid padding")
	}
	return plain[:len(plain)-padding], nil
}

func verifyFeishuCallbackSignature(r *http.Request, body []byte, encryptKey string) bool {
	timestamp := r.Header.Get("X-Lark-Request-Timestamp")
	nonce := r.Header.Get("X-Lark-Request-Nonce")
	provided := strings.TrimSpace(r.Header.Get("X-Lark-Signature"))
	if timestamp == "" || nonce == "" || provided == "" {
		return false
	}
	content := timestamp + nonce + encryptKey + string(body)
	sum := sha256.Sum256([]byte(content))
	expected := hex.EncodeToString(sum[:])
	if len(provided) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(provided)), []byte(expected)) == 1
}
