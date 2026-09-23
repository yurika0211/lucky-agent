package feishu

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/gateway"
)

func TestDecryptFeishuEventOfficialFixture(t *testing.T) {
	plain, err := decryptFeishuEvent("test key", "P37w+VZImNgPEO1RBhJ6RtKl7n6zymIbEG1pReEzghk=")
	if err != nil {
		t.Fatalf("decryptFeishuEvent: %v", err)
	}
	if string(plain) != "hello world" {
		t.Fatalf("decrypted payload = %q, want hello world", plain)
	}
}

func TestCallbackDecryptsAndVerifiesEncryptedEvent(t *testing.T) {
	cfg := callbackTestConfig()
	cfg.EncryptKey = "callback-encrypt-key"
	a := NewAdapter(cfg)
	received := make(chan *gateway.Message, 1)
	a.SetHandler(func(_ context.Context, msg *gateway.Message) error {
		received <- msg
		return nil
	})

	plain, err := json.Marshal(schemaV2Payload("p2p", "encrypted hello", nil))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := encryptFeishuEventForTest(t, cfg.EncryptKey, plain)
	body, _ := json.Marshal(map[string]string{"encrypt": ciphertext})
	req := signedFeishuRequest(t, a, body, cfg.EncryptKey)
	recorder := httptest.NewRecorder()
	a.handleCallback(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("callback status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	select {
	case msg := <-received:
		if msg.Text != "encrypted hello" {
			t.Fatalf("message text = %q", msg.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("encrypted event was not dispatched")
	}
}

func TestEncryptedURLChallengeDoesNotRequireCallbackSignature(t *testing.T) {
	cfg := callbackTestConfig()
	cfg.EncryptKey = "callback-encrypt-key"
	a := NewAdapter(cfg)
	plain, _ := json.Marshal(map[string]any{"type": "url_verification", "token": "verify-me", "challenge": "challenge-value"})
	body, _ := json.Marshal(map[string]string{"encrypt": encryptFeishuEventForTest(t, cfg.EncryptKey, plain)})
	req := httptest.NewRequest(http.MethodPost, "http://callback.test"+a.Path(), bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	a.handleCallback(recorder, req)
	var response map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if recorder.Code != http.StatusOK || response["challenge"] != "challenge-value" {
		t.Fatalf("challenge response status=%d body=%#v", recorder.Code, response)
	}
}

func TestEncryptedCallbackRejectsInvalidSignature(t *testing.T) {
	cfg := callbackTestConfig()
	cfg.EncryptKey = "callback-encrypt-key"
	a := NewAdapter(cfg)
	plain, _ := json.Marshal(schemaV2Payload("p2p", "hello", nil))
	body, _ := json.Marshal(map[string]string{"encrypt": encryptFeishuEventForTest(t, cfg.EncryptKey, plain)})
	req := httptest.NewRequest(http.MethodPost, "http://callback.test"+a.Path(), bytes.NewReader(body))
	req.Header.Set("X-Lark-Request-Timestamp", "1")
	req.Header.Set("X-Lark-Request-Nonce", "nonce")
	req.Header.Set("X-Lark-Signature", "invalid")
	recorder := httptest.NewRecorder()
	a.handleCallback(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("invalid signature status = %d", recorder.Code)
	}
}

func encryptFeishuEventForTest(t *testing.T, key string, plain []byte) string {
	t.Helper()
	hash := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(hash[:])
	if err != nil {
		t.Fatal(err)
	}
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(append([]byte(nil), plain...), bytes.Repeat([]byte{byte(padding)}, padding)...)
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	encrypted := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(encrypted, plain)
	return base64.StdEncoding.EncodeToString(append(iv, encrypted...))
}

func signedFeishuRequest(t *testing.T, a *Adapter, body []byte, encryptKey string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://callback.test"+a.Path(), bytes.NewReader(body))
	timestamp, nonce := "1727091200", "test-nonce"
	sum := sha256.Sum256([]byte(timestamp + nonce + encryptKey + string(body)))
	req.Header.Set("X-Lark-Request-Timestamp", timestamp)
	req.Header.Set("X-Lark-Request-Nonce", nonce)
	req.Header.Set("X-Lark-Signature", hex.EncodeToString(sum[:]))
	return req
}
