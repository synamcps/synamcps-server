package domainerr

import "errors"

var (
	ErrForbidden      = errors.New("forbidden")
	ErrNotFound       = errors.New("not found")
	ErrRateLimited    = errors.New("rate limited")
	ErrUnknownMethod  = errors.New("unknown method")
)
