package wechat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSessionJSONDoesNotExposeSessionKey(t *testing.T) {
	encoded, err := json.Marshal(Session{
		OpenID:     "wx-openid",
		UnionID:    "wx-unionid",
		SessionKey: "wx-session-key",
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), "wx-session-key") || strings.Contains(string(encoded), "SessionKey") || strings.Contains(string(encoded), "session_key") {
		t.Fatalf("serialized session exposes session key: %s", encoded)
	}
}

func TestFakeClientReturnsDeterministicSessionWithoutPersistenceFields(t *testing.T) {
	client := NewFakeClient()

	session, err := client.Code2Session(context.Background(), "fixture-code")
	if err != nil {
		t.Fatalf("Code2Session() error = %v", err)
	}
	if session.OpenID != "fake-openid" {
		t.Fatalf("OpenID = %q, want fake-openid", session.OpenID)
	}
	if session.UnionID != "fake-unionid" {
		t.Fatalf("UnionID = %q, want fake-unionid", session.UnionID)
	}
	if session.SessionKey != "fake-session-key" {
		t.Fatalf("SessionKey = %q, want fake-session-key", session.SessionKey)
	}
}

func TestFakeClientReturnsInjectedError(t *testing.T) {
	wantErr := errors.New("fake code2session failure")
	client := NewFakeClientWithError(wantErr)

	_, err := client.Code2Session(context.Background(), "fixture-code")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Code2Session() error = %v, want %v", err, wantErr)
	}
}

func TestHTTPClientPostsCode2SessionRequestAndParsesSession(t *testing.T) {
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q, want form content type", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm() error = %v", err)
			return
		}
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0,"openid":"wx-openid","unionid":"wx-unionid","session_key":"wx-session-key"}`))
	}))
	defer server.Close()

	client, err := NewWeChatClient("app-id", "app-secret", server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewWeChatClient() error = %v", err)
	}

	session, err := client.Code2Session(context.Background(), "one-time-code")
	if err != nil {
		t.Fatalf("Code2Session() error = %v", err)
	}
	if session != (Session{OpenID: "wx-openid", UnionID: "wx-unionid", SessionKey: "wx-session-key"}) {
		t.Fatalf("session = %#v, want parsed WeChat session", session)
	}
	for key, want := range map[string]string{
		"appid":      "app-id",
		"secret":     "app-secret",
		"js_code":    "one-time-code",
		"grant_type": "authorization_code",
	} {
		if got := gotForm.Get(key); got != want {
			t.Errorf("form %s = %q, want %q", key, got, want)
		}
	}
}

func TestHTTPClientDoesNotExposeSessionKeyInError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"errcode":-1,"errmsg":"session_key=wx-secret-key"}`))
	}))
	defer server.Close()

	client, err := NewWeChatClient("app-id", "app-secret", server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewWeChatClient() error = %v", err)
	}
	_, err = client.Code2Session(context.Background(), "one-time-code")
	if err == nil {
		t.Fatal("Code2Session() error = nil, want HTTP error")
	}
	if strings.Contains(err.Error(), "wx-secret-key") {
		t.Fatalf("error contains session key: %v", err)
	}
}

func TestNewWeChatClientRejectsHardcodedOrInvalidEndpointInput(t *testing.T) {
	for _, endpoint := range []string{"", "://bad", "ftp://wechat.example.test/session"} {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := NewWeChatClient("app-id", "app-secret", endpoint, nil); err == nil {
				t.Fatalf("NewWeChatClient(%q) error = nil", endpoint)
			}
		})
	}
}

// TestNewClientResolvesFakeOrRealByConfiguration proves the composition-root
// resolver returns the deterministic fake when no credentials are configured
// and errors on a partial configuration, so a deploy can never silently fall
// back to the fake.
func TestNewClientResolvesFakeOrRealByConfiguration(t *testing.T) {
	fake, err := NewClient("", "", "", nil)
	if err != nil {
		t.Fatalf("NewClient with empty config: %v", err)
	}
	if _, ok := fake.(*FakeClient); !ok {
		t.Fatalf("NewClient with empty config returned %T, want *FakeClient", fake)
	}

	if _, err := NewClient("app-id", "", "https://api.weixin.qq.com/sns/jscode2session", nil); err == nil {
		t.Fatal("NewClient accepted a partial configuration")
	}

	real, err := NewClient("app-id", "app-secret", "https://api.weixin.qq.com/sns/jscode2session", nil)
	if err != nil {
		t.Fatalf("NewClient with full config: %v", err)
	}
	if _, ok := real.(*WeChatClient); !ok {
		t.Fatalf("NewClient with full config returned %T, want *WeChatClient", real)
	}
}
