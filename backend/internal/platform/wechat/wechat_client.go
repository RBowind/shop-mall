package wechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const maxCode2SessionResponseBytes = 1 << 20

// NewClient resolves the configured WeChat integration. When no AppID, Secret
// or endpoint are configured (the local default) it returns the deterministic
// FakeClient so a developer machine and the test suites never touch the real
// WeChat API. When any credential is present it builds the real client and
// returns an error if the configuration is incomplete. The AppID and Secret
// are deploy-only values and must never be embedded in code.
func NewClient(appID, secret, endpoint string, httpClient *http.Client) (Client, error) {
	if appID == "" && secret == "" && endpoint == "" {
		return NewFakeClient(), nil
	}
	return NewWeChatClient(appID, secret, endpoint, httpClient)
}

type WeChatClient struct {
	appID      string
	secret     string
	endpoint   string
	httpClient *http.Client
}

var _ Client = (*WeChatClient)(nil)

func NewWeChatClient(appID, secret, endpoint string, httpClient *http.Client) (*WeChatClient, error) {
	if strings.TrimSpace(appID) == "" {
		return nil, errors.New("WeChat app ID is required")
	}
	if strings.TrimSpace(secret) == "" {
		return nil, errors.New("WeChat app secret is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("WeChat endpoint must be an HTTP or HTTPS URL")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &WeChatClient{
		appID:      appID,
		secret:     secret,
		endpoint:   parsed.String(),
		httpClient: httpClient,
	}, nil
}

func (c *WeChatClient) Code2Session(ctx context.Context, code string) (Session, error) {
	if c == nil {
		return Session{}, errors.New("WeChat client is nil")
	}
	if ctx == nil {
		return Session{}, errors.New("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	if strings.TrimSpace(code) == "" {
		return Session{}, errors.New("WeChat login code is required")
	}

	form := url.Values{}
	form.Set("appid", c.appID)
	form.Set("secret", c.secret)
	form.Set("js_code", code)
	form.Set("grant_type", "authorization_code")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Session{}, fmt.Errorf("create WeChat request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return Session{}, fmt.Errorf("call WeChat code2Session: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxCode2SessionResponseBytes+1))
	if err != nil {
		return Session{}, errors.New("read WeChat code2Session response")
	}
	if len(body) > maxCode2SessionResponseBytes {
		return Session{}, errors.New("WeChat code2Session response is too large")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Session{}, fmt.Errorf("WeChat code2Session returned HTTP status %d", response.StatusCode)
	}

	var payload struct {
		ErrCode    int    `json:"errcode"`
		OpenID     string `json:"openid"`
		UnionID    string `json:"unionid"`
		SessionKey string `json:"session_key"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Session{}, errors.New("decode WeChat code2Session response")
	}
	if payload.ErrCode != 0 {
		return Session{}, fmt.Errorf("WeChat code2Session failed with error code %d", payload.ErrCode)
	}
	if strings.TrimSpace(payload.OpenID) == "" {
		return Session{}, errors.New("WeChat code2Session response has no openid")
	}
	return Session{
		OpenID:     payload.OpenID,
		UnionID:    payload.UnionID,
		SessionKey: payload.SessionKey,
	}, nil
}
