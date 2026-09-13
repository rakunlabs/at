package common

import (
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

// ValidateSingleChoice prevents paying for multiple upstream candidates while
// the internal response/stream contract can represent only one choice.
func ValidateSingleChoice(opts *service.ChatOptions) error {
	if opts != nil && opts.N != nil && *opts.N != 1 {
		return &service.UpstreamError{
			StatusCode: http.StatusBadRequest,
			Code:       "unsupported_value", Param: "n",
			Message: "AT currently supports only n=1; multiple response choices cannot be returned",
		}
	}
	return nil
}
