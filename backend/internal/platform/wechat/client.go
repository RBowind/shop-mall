package wechat

import "context"

// Session is the short-lived result returned by WeChat code2Session.
// SessionKey must be consumed and discarded by the caller; it is not a
// persistence or response model.
type Session struct {
	OpenID     string `json:"openid"`
	UnionID    string `json:"unionid,omitempty"`
	SessionKey string `json:"-"`
}

type Client interface {
	Code2Session(ctx context.Context, code string) (Session, error)
}
