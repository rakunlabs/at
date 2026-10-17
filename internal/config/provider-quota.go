package config

import "fmt"

// QuotaLimits are local admission ceilings, not authoritative upstream balances.
type QuotaLimits struct {
	RequestsPerDay   int64 `json:"requests_per_day,omitempty"`
	TokensPerDay     int64 `json:"tokens_per_day,omitempty"`
	RequestsPerMonth int64 `json:"requests_per_month,omitempty"`
	TokensPerMonth   int64 `json:"tokens_per_month,omitempty"`
}

type ProviderQuotaConfig struct {
	QuotaLimits
	Models map[string]QuotaLimits `json:"models,omitempty"`
}

func (q *ProviderQuotaConfig) HasLimits() bool {
	if q == nil {
		return false
	}
	check := func(v QuotaLimits) bool {
		return v.RequestsPerDay > 0 || v.TokensPerDay > 0 || v.RequestsPerMonth > 0 || v.TokensPerMonth > 0
	}
	if check(q.QuotaLimits) {
		return true
	}
	for _, v := range q.Models {
		if check(v) {
			return true
		}
	}
	return false
}

func (q *ProviderQuotaConfig) HasTokenLimits() bool {
	if q == nil {
		return false
	}
	if q.TokensPerDay > 0 || q.TokensPerMonth > 0 {
		return true
	}
	for _, v := range q.Models {
		if v.TokensPerDay > 0 || v.TokensPerMonth > 0 {
			return true
		}
	}
	return false
}

func (q *ProviderQuotaConfig) Validate() error {
	if q == nil {
		return nil
	}
	check := func(v QuotaLimits) error {
		for _, n := range []int64{v.RequestsPerDay, v.TokensPerDay, v.RequestsPerMonth, v.TokensPerMonth} {
			if n < 0 || n > 1_000_000_000_000 {
				return fmt.Errorf("quota limits must be between 0 and 1000000000000 (0 means unlimited)")
			}
		}
		return nil
	}
	if err := check(q.QuotaLimits); err != nil {
		return err
	}
	if len(q.Models) > 500 {
		return fmt.Errorf("quota allows at most 500 model pools")
	}
	for model, v := range q.Models {
		if model == "" || len(model) > 500 {
			return fmt.Errorf("quota model name is invalid")
		}
		if err := check(v); err != nil {
			return fmt.Errorf("quota for %s: %w", model, err)
		}
	}
	return nil
}
