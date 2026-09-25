package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/rakunlabs/query"
)

const (
	BudgetResourceProvider        = "provider"
	BudgetResourceVirtualProvider = "virtual_provider"

	BudgetOverrideCustom    = "custom"
	BudgetOverrideUnlimited = "unlimited"
	BudgetOverrideBlocked   = "blocked"
)

var (
	ErrProviderBudgetExceeded  = errors.New("provider budget exceeded")
	ErrProviderUserLimit       = errors.New("provider user budget exceeded")
	ErrProviderUserBlocked     = errors.New("user is blocked from this provider")
	ErrProviderPricingRequired = errors.New("provider budget requires model pricing")
)

type ProviderBudgetPolicy struct {
	ID                    string  `json:"id"`
	ResourceKind          string  `json:"resource_kind"`
	ResourceID            string  `json:"resource_id"`
	TotalLimitCents       float64 `json:"total_limit_cents"`
	DefaultUserLimitCents float64 `json:"default_user_limit_cents"`
	BudgetSchedule
	EnforceUnpriced bool   `json:"enforce_unpriced"`
	CreatedAt       string `json:"created_at,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
	CreatedBy       string `json:"created_by,omitempty"`
	UpdatedBy       string `json:"updated_by,omitempty"`
}

type ProviderBudgetOverride struct {
	PolicyID   string  `json:"policy_id"`
	UserID     string  `json:"user_id"`
	Mode       string  `json:"mode"`
	LimitCents float64 `json:"limit_cents"`
	UpdatedAt  string  `json:"updated_at,omitempty"`
	UpdatedBy  string  `json:"updated_by,omitempty"`
}

type ProviderBudgetStatus struct {
	ProviderBudgetPolicy
	PeriodStart        string  `json:"period_start"`
	PeriodEnd          string  `json:"period_end"`
	SpentCents         float64 `json:"spent_cents"`
	ReservedCents      float64 `json:"reserved_cents"`
	UserSpentCents     float64 `json:"user_spent_cents,omitempty"`
	UserReservedCents  float64 `json:"user_reserved_cents,omitempty"`
	EffectiveUserLimit float64 `json:"effective_user_limit_cents,omitempty"`
}

type BudgetResource struct {
	Kind string
	ID   string
}

type ProviderBudgetReservation struct {
	ID string
}

type ProviderBudgetStorer interface {
	GetProviderBudgetPolicy(context.Context, string, string) (*ProviderBudgetPolicy, error)
	SaveProviderBudgetPolicy(context.Context, ProviderBudgetPolicy) (*ProviderBudgetPolicy, error)
	DeleteProviderBudgetPolicy(context.Context, string, string) error
	ListProviderBudgetOverrides(context.Context, string) ([]ProviderBudgetOverride, error)
	SaveProviderBudgetOverride(context.Context, ProviderBudgetOverride) error
	DeleteProviderBudgetOverride(context.Context, string, string) error
	GetProviderBudgetStatus(context.Context, string, string, string) (*ProviderBudgetStatus, error)
	ReserveProviderBudget(context.Context, []BudgetResource, string, *float64) (*ProviderBudgetReservation, error)
	SettleProviderBudget(context.Context, string, float64) error
}

type VirtualProviderModel struct {
	Alias       string `json:"alias"`
	ProviderRef string `json:"provider_ref"`
	Model       string `json:"model"`
	Position    int    `json:"position"`
}

type VirtualProvider struct {
	WorkspaceID  string                 `json:"workspace_id"`
	ID           string                 `json:"id"`
	Key          string                 `json:"key"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	DefaultModel string                 `json:"default_model"`
	Disabled     bool                   `json:"disabled"`
	Models       []VirtualProviderModel `json:"models"`
	CreatedAt    string                 `json:"created_at,omitempty"`
	UpdatedAt    string                 `json:"updated_at,omitempty"`
	CreatedBy    string                 `json:"created_by,omitempty"`
	UpdatedBy    string                 `json:"updated_by,omitempty"`
}

type VirtualProviderGrant struct {
	ID                 string   `json:"id"`
	VirtualProviderID  string   `json:"virtual_provider_id"`
	WorkspaceID        string   `json:"workspace_id"`
	ModelPatterns      []string `json:"model_patterns"`
	AllowUserOverrides bool     `json:"allow_user_overrides"`
	MaxUserLimitCents  float64  `json:"max_user_limit_cents"`
	CreatedAt          string   `json:"created_at,omitempty"`
	CreatedBy          string   `json:"created_by,omitempty"`
}

type ProviderRoute struct {
	Record            ProviderRecord
	ActualModel       string
	VirtualProviderID string
	VirtualKey        string
}

type VirtualProviderStorer interface {
	ListVirtualProviders(context.Context, *query.Query) (*ListResult[VirtualProvider], error)
	GetVirtualProvider(context.Context, string) (*VirtualProvider, error)
	CreateVirtualProvider(context.Context, VirtualProvider) (*VirtualProvider, error)
	UpdateVirtualProvider(context.Context, string, VirtualProvider) (*VirtualProvider, error)
	DeleteVirtualProvider(context.Context, string) error
	ListVirtualProviderGrants(context.Context, string) ([]VirtualProviderGrant, error)
	SaveVirtualProviderGrant(context.Context, VirtualProviderGrant) (*VirtualProviderGrant, error)
	DeleteVirtualProviderGrant(context.Context, string, string) error
}

type ProviderRouteStorer interface {
	ResolveWorkspaceProviderRoute(context.Context, string, string) (*ProviderRoute, error)
	ResolveGatewayProviderRoute(context.Context, string, string, string, string) (*ProviderRoute, error)
	ListGatewayVirtualProviderCatalog(context.Context, string, string) ([]ProviderCatalogEntry, error)
}

var virtualProviderKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func ValidateVirtualProvider(v VirtualProvider) error {
	v.Key = strings.TrimSpace(v.Key)
	v.Name = strings.TrimSpace(v.Name)
	if v.Key == "" || len(v.Key) > 100 || !virtualProviderKeyPattern.MatchString(v.Key) {
		return fmt.Errorf("key must be 1-100 letters, digits, dots, underscores or hyphens")
	}
	if v.Name == "" || len(v.Name) > 200 {
		return fmt.Errorf("name is required and must be at most 200 characters")
	}
	if len(v.Models) == 0 || len(v.Models) > 64 {
		return fmt.Errorf("between 1 and 64 models is required")
	}
	seen := map[string]bool{}
	defaultFound := false
	for i, model := range v.Models {
		model.Alias = strings.TrimSpace(model.Alias)
		if model.Alias == "" || strings.Contains(model.Alias, "/") || !virtualProviderKeyPattern.MatchString(model.Alias) {
			return fmt.Errorf("model %d alias is invalid", i+1)
		}
		if model.ProviderRef == "" || model.Model == "" {
			return fmt.Errorf("model %q requires provider_ref and model", model.Alias)
		}
		if seen[model.Alias] {
			return fmt.Errorf("model alias %q is duplicated", model.Alias)
		}
		seen[model.Alias] = true
		defaultFound = defaultFound || model.Alias == v.DefaultModel
	}
	if !defaultFound {
		return fmt.Errorf("default_model must name one of the model aliases")
	}
	return nil
}
