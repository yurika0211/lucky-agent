package weixin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	epGetBotQRCode   = "ilink/bot/get_bot_qrcode"
	epGetQRCodeState = "ilink/bot/get_qrcode_status"
)

type LoginClient struct {
	baseURL    string
	httpClient *http.Client
}

type QRCodeLogin struct {
	QRCode      string
	QRCodeURL   string
	RawImage    []byte
	Status      string
	AccountID   string
	Token       string
	BaseURL     string
	ExpiresAt   time.Time
	Description string
}

type qrCodeResponse struct {
	QRCode string `json:"qrcode"`
	URL    string `json:"url"`
	Image  string `json:"image"`
}

type qrCodeStatusResponse struct {
	Status      string `json:"status"`
	AccountID   string `json:"account_id"`
	BotToken    string `json:"bot_token"`
	BaseURL     string `json:"baseurl"`
	Description string `json:"desc"`
	Message     string `json:"message"`
}

func NewLoginClient(baseURL string) *LoginClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultBaseURL
	}
	return &LoginClient{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *LoginClient) FetchQRCode(ctx context.Context) (*QRCodeLogin, error) {
	u := c.baseURL + "/" + epGetBotQRCode + "?bot_type=3"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out qrCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode weixin qrcode response: %w", err)
	}
	if strings.TrimSpace(out.QRCode) == "" {
		return nil, fmt.Errorf("weixin login: empty qrcode response")
	}

	login := &QRCodeLogin{
		QRCode:    strings.TrimSpace(out.QRCode),
		QRCodeURL: strings.TrimSpace(out.URL),
		BaseURL:   c.baseURL,
	}
	if raw := strings.TrimSpace(out.Image); raw != "" {
		decoded, err := decodeMaybeDataURL(raw)
		if err == nil {
			login.RawImage = decoded
		}
	}
	if login.QRCodeURL == "" {
		login.QRCodeURL = login.QRCode
	}
	return login, nil
}

func (c *LoginClient) GetQRCodeStatus(ctx context.Context, qrCode string) (*QRCodeLogin, error) {
	u := c.baseURL + "/" + epGetQRCodeState + "?qrcode=" + url.QueryEscape(strings.TrimSpace(qrCode))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out qrCodeStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode weixin qrcode status: %w", err)
	}

	desc := strings.TrimSpace(out.Description)
	if desc == "" {
		desc = strings.TrimSpace(out.Message)
	}
	baseURL := strings.TrimSpace(out.BaseURL)
	if baseURL == "" {
		baseURL = c.baseURL
	}
	return &QRCodeLogin{
		QRCode:      strings.TrimSpace(qrCode),
		Status:      strings.TrimSpace(out.Status),
		AccountID:   strings.TrimSpace(out.AccountID),
		Token:       strings.TrimSpace(out.BotToken),
		BaseURL:     strings.TrimRight(baseURL, "/"),
		Description: desc,
	}, nil
}

func decodeMaybeDataURL(v string) ([]byte, error) {
	raw := strings.TrimSpace(v)
	if raw == "" {
		return nil, fmt.Errorf("empty image")
	}
	if idx := strings.Index(raw, ","); strings.HasPrefix(raw, "data:") && idx >= 0 {
		raw = raw[idx+1:]
	}
	return base64.StdEncoding.DecodeString(raw)
}
