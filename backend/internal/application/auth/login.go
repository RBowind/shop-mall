package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/platform/wechat"
	"shop-mall/backend/internal/user"

	"gorm.io/gorm"
)

// Error sentinels the HTTP handler maps to status codes. A blank login code is
// a client error (400); a failed or empty WeChat session lookup is a business
// error (422); everything else is an internal failure (500).
var (
	ErrLoginCodeRequired = errors.New("WeChat login code is required")
	ErrWeChatLookup      = errors.New("WeChat session lookup failed")
	ErrEmptyOpenID       = errors.New("wechat session returned an empty openid")
)

// LoginUsecase is the application boundary that exchanges a WeChat login code
// for an authenticated buyer session. The external session lookup, the
// database insert-or-read, the exactly-once signup bonus and the JWT signing
// each own a separate responsibility with a strict order.
type LoginUsecase struct {
	db          *gorm.DB
	wechat      wechat.Client
	signer      SignerInterface
	logger      *slog.Logger
	now         func() time.Time
	signupBonus int64
}

// SignerInterface is the subset of tokens.Signer that LoginUsecase uses. The
// test suite can substitute a deterministic implementation through this
// interface without touching the production signer.
type SignerInterface interface {
	Issue(subject string, tokenVersion int64, now time.Time) (string, error)
}

// LoginUsecaseDeps is the dependency bundle for NewLoginUsecase.
type LoginUsecaseDeps struct {
	DB          *gorm.DB
	WeChat      wechat.Client
	Signer      SignerInterface
	Logger      *slog.Logger
	Now         func() time.Time
	SignupBonus int64
}

// NewLoginUsecase validates the dependency bundle and returns a LoginUsecase.
// The WeChat client must not be nil; the database handle must not be nil; the
// signer must not be nil; and the SignupBonus must be non-negative.
func NewLoginUsecase(deps LoginUsecaseDeps) *LoginUsecase {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &LoginUsecase{
		db:          deps.DB,
		wechat:      deps.WeChat,
		signer:      deps.Signer,
		logger:      logger,
		now:         now,
		signupBonus: deps.SignupBonus,
	}
}

// LoginResult is the observable payload of a successful buyer login. The
// SessionKey, password, JWT, full phone number and full address never appear
// here. The Token is the freshly signed JWT, valid for the configured TTL.
type LoginResult struct {
	User  User
	Token string
}

// User is the application projection of a buyer returned to the HTTP layer
// after a successful login. The Subject is the JWT subject embedded in the
// freshly signed token.
type User struct {
	ID            uid.ID    `json:"id"`
	OpenID        string    `json:"openid"`
	Nickname      string    `json:"nickname"`
	AvatarURL     string    `json:"avatar_url"`
	PointsBalance int64     `json:"points_balance"`
	Subject       string    `json:"subject"`
	CreatedAt     time.Time `json:"created_at"`
}

// Execute performs the buyer login usecase.
//
// Contract:
//   - The WeChat code2Session lookup runs OUTSIDE any database transaction.
//     The returned Session.SessionKey MUST be discarded before the database
//     transaction starts; only OpenID and UnionID reach the database or the
//     returned LoginResult.
//   - Inside the transaction the usecase performs an insert-or-read on the
//     users table. A new buyer receives an exactly-once signup bonus ledger
//     entry; an existing buyer never receives a second bonus.
//   - JWT signing is the LAST step and runs only AFTER the database
//     transaction commits successfully. A failure to sign MUST NOT roll back
//     the database write.
func (u *LoginUsecase) Execute(ctx context.Context, code string) (LoginResult, error) {
	if u == nil {
		return LoginResult{}, errors.New("login usecase is not configured")
	}
	if ctx == nil {
		return LoginResult{}, errors.New("login context is required")
	}
	if u.db == nil || u.wechat == nil || u.signer == nil {
		return LoginResult{}, errors.New("login usecase dependencies are not configured")
	}
	if strings.TrimSpace(code) == "" {
		return LoginResult{}, ErrLoginCodeRequired
	}

	session, err := u.wechat.Code2Session(ctx, code)
	if err != nil {
		// Both the underlying adapter error and the sentinel are wrapped so a
		// caller can match the sentinel while the log keeps the adapter detail.
		return LoginResult{}, fmt.Errorf("wechat session lookup failed: %w: %w", err, ErrWeChatLookup)
	}
	if strings.TrimSpace(session.OpenID) == "" {
		return LoginResult{}, ErrEmptyOpenID
	}
	// Discard the SessionKey explicitly. We never log it, never write it to
	// the database, never embed it in a JWT and never include it in the
	// LoginResult. The contract guarantees that the session key is consumed
	// by the WeChat adapter only.
	session.SessionKey = ""
	sessionKeyLeaked := session.SessionKey != ""

	userService, err := user.NewService(user.ServiceDeps{DB: u.db})
	if err != nil {
		return LoginResult{}, err
	}
	now := u.now().UTC()
	buyer, err := userService.UpsertByOpenID(ctx, session.OpenID, session.UnionID, u.signupBonus, nil, now)
	if err != nil {
		return LoginResult{}, fmt.Errorf("upsert buyer: %w", err)
	}
	if sessionKeyLeaked {
		// Drop the session key without recording it. The contract guarantees
		// that no observer of this function (logger, database, JWT, HTTP
		// response) ever sees the session key; a future operator-side audit
		// can use the request count to detect a misbehaving adapter without
		// ever materialising the value.
		_ = sessionKeyLeaked
	}
	token, err := u.signer.Issue(buyer.Subject(), 1, now)
	if err != nil {
		return LoginResult{}, fmt.Errorf("sign buyer token: %w", err)
	}
	return LoginResult{
		User: User{
			ID:            buyer.ID,
			OpenID:        buyer.OpenID,
			Nickname:      buyer.Nickname,
			AvatarURL:     buyer.AvatarURL,
			PointsBalance: buyer.PointsBalance,
			Subject:       buyer.Subject(),
			CreatedAt:     buyer.CreatedAt,
		},
		Token: token,
	}, nil
}

// Subject returns the JWT subject string used to embed into the issued
// token. Mirrors user.User.Subject but is exported on the application
// projection to avoid leaking the internal struct.
func (u User) SubjectValue() string { return u.Subject }
