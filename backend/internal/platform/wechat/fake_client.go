package wechat

import (
	"context"
	"errors"
	"strings"
)

const (
	defaultFakeOpenID     = "fake-openid"
	defaultFakeUnionID    = "fake-unionid"
	defaultFakeSessionKey = "fake-session-key"
)

// FakeClient is a deterministic local-only implementation of Client. It has
// no database, logger, or network dependency, so its session key cannot leave
// this adapter through an infrastructure side effect.
type FakeClient struct {
	OpenID     string
	UnionID    string
	SessionKey string
	Err        error
}

var _ Client = (*FakeClient)(nil)

func NewFakeClient() *FakeClient {
	return &FakeClient{
		OpenID:     defaultFakeOpenID,
		UnionID:    defaultFakeUnionID,
		SessionKey: defaultFakeSessionKey,
	}
}

func NewFakeClientWithError(err error) *FakeClient {
	client := NewFakeClient()
	client.Err = err
	return client
}

func NewFakeClientWithSession(openID, unionID, sessionKey string) *FakeClient {
	return &FakeClient{OpenID: openID, UnionID: unionID, SessionKey: sessionKey}
}

func (c *FakeClient) Code2Session(ctx context.Context, code string) (Session, error) {
	if c == nil {
		return Session{}, errors.New("fake WeChat client is nil")
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
	if c.Err != nil {
		return Session{}, c.Err
	}
	return Session{
		OpenID:     c.OpenID,
		UnionID:    c.UnionID,
		SessionKey: c.SessionKey,
	}, nil
}
