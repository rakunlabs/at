package nativeauth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/identity"
	"github.com/rakunlabs/ada/middleware/auth/strategy"
	"github.com/rakunlabs/ada/middleware/auth/strategy/bearer"

	"github.com/rakunlabs/at/internal/service"
)

// Token exchange: a trusted backend presents a token an identity provider
// issued to a person, and AT resolves the person's account from it without a
// browser session. Verification is local (bearer strategy): signature against
// the provider's JWKS, exact issuer, an accepted audience, exp and nbf. The
// same allowed-email admission and claim assertions as a browser sign-in
// apply, so workspace permission mappings and claim admission treat both
// paths identically.

var (
	// ErrExchangeRejected is a token that is not acceptable: bad signature,
	// issuer, audience, expiry, an unknown provider or a refused address.
	ErrExchangeRejected = errors.New("subject token rejected")
	// ErrExchangeUnknownUser is a valid token for a person who has never
	// signed in while the provider does not provision on exchange.
	ErrExchangeUnknownUser = errors.New("this person has not signed in to AT yet")
)

// ExchangeResult is the account a subject token resolved to.
type ExchangeResult struct {
	User     *service.AuthUser
	Link     *service.AuthIdentityLink
	Provider service.AuthIdentityProvider
	// ExpiresAt is the subject token's expiry; a run should not outlive it
	// by much, and a cached exchange never does.
	ExpiresAt time.Time
}

// TokenExchanger verifies subject tokens for providers with TokenExchange.
type TokenExchanger struct {
	store    service.AuthExternalStorer
	loopback bool
	client   *http.Client

	mu         sync.Mutex
	strategies map[string]exchangeStrategy
}

type exchangeStrategy struct {
	version int64
	s       *bearer.Strategy
}

// NewTokenExchanger returns nil when the store does not persist providers.
func NewTokenExchanger(store any, loopback bool) *TokenExchanger {
	s, ok := store.(service.AuthExternalStorer)
	if !ok {
		return nil
	}
	return &TokenExchanger{
		store:      s,
		loopback:   loopback,
		client:     &http.Client{Timeout: 10 * time.Second, Transport: externalTransport{loopback: loopback}},
		strategies: map[string]exchangeStrategy{},
	}
}

func (x *TokenExchanger) strategy(p service.AuthIdentityProvider) (*bearer.Strategy, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if cached, ok := x.strategies[p.ID]; ok && cached.version == p.Version {
		return cached.s, nil
	}
	claims := bearer.ClaimMap{Subject: p.SubjectClaim, Roles: append([]string{"roles"}, p.RolesClaims...)}
	s, err := bearer.New(bearer.Config{Issuer: p.Issuer, JWKSURL: p.JWKSURL, Audience: p.Audiences, Claims: claims}, bearer.Options{Name: "exchange:" + p.ID, HTTPClient: x.client})
	if err != nil {
		return nil, err
	}
	x.strategies[p.ID] = exchangeStrategy{version: p.Version, s: s}
	return s, nil
}

// Exchange verifies subjectToken for providerID and returns the account.
// It never creates a session.
func (x *TokenExchanger) Exchange(ctx context.Context, providerID, subjectToken string) (*ExchangeResult, error) {
	if x == nil {
		return nil, fmt.Errorf("%w: token exchange is not available", ErrExchangeRejected)
	}
	subjectToken = strings.TrimSpace(subjectToken)
	if subjectToken == "" || len(subjectToken) > 16<<10 {
		return nil, fmt.Errorf("%w: missing or oversized subject token", ErrExchangeRejected)
	}
	p, err := x.store.GetAuthIdentityProvider(ctx, providerID)
	if err != nil {
		return nil, err
	}
	if p == nil || !p.Enabled || !p.TokenExchange {
		return nil, fmt.Errorf("%w: unknown identity provider or token exchange is off", ErrExchangeRejected)
	}
	if err := validateExternalURL(p.JWKSURL, x.loopback); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrExchangeRejected, err)
	}
	s, err := x.strategy(*p)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrExchangeRejected, err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+subjectToken)
	id, err := s.AuthenticateRequest(ctx, req)
	if err != nil {
		if errors.Is(err, strategy.ErrInvalidCredentials) || errors.Is(err, strategy.ErrNoCredentials) {
			slog.Info("token exchange rejected", "provider", p.ID, "error", err.Error())
			return nil, fmt.Errorf("%w: the token is invalid, expired or not issued for this audience", ErrExchangeRejected)
		}
		return nil, fmt.Errorf("verify subject token: %w", err)
	}
	return x.complete(ctx, *p, id)
}

func (x *TokenExchanger) complete(ctx context.Context, p service.AuthIdentityProvider, id *identity.Identity) (*ExchangeResult, error) {
	if id == nil || id.Subject == "" {
		return nil, fmt.Errorf("%w: no subject", ErrExchangeRejected)
	}
	link := ExternalIdentityLink(p, id)
	if err := service.AuthEmailAdmits(p.AllowedEmails, link.Email, link.EmailVerified); err != nil {
		slog.Warn("token exchange refused by allowed emails", "provider", p.ID, "subject", id.Subject)
		return nil, fmt.Errorf("%w: %v", ErrExchangeRejected, err)
	}
	if !p.ExchangeProvisioning {
		known, err := x.linked(ctx, p.ID, id.Subject)
		if err != nil {
			return nil, err
		}
		if !known {
			return nil, ErrExchangeUnknownUser
		}
	}
	user, stored, err := x.store.CompleteAuthExternalIdentity(ctx, link, p.Version, nil, time.Now().Add(time.Minute))
	if err != nil {
		if errors.Is(err, service.ErrAuthConflict) {
			return nil, fmt.Errorf("%w: the account is disabled or the provider changed", ErrExchangeRejected)
		}
		return nil, err
	}
	return &ExchangeResult{User: user, Link: stored, Provider: p, ExpiresAt: id.ExpiresAt}, nil
}

func (x *TokenExchanger) linked(ctx context.Context, providerID, subject string) (bool, error) {
	finder, ok := x.store.(service.AuthIdentityLinkFinder)
	if !ok {
		return false, errors.New("identity link lookup unavailable")
	}
	return finder.HasAuthIdentityLink(ctx, providerID, service.AuthIdentityNamespace(providerID), subject)
}
