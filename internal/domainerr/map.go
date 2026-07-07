package domainerr

import (
	"errors"
	"net/http"
)

// HTTPStatus maps a domain error to an HTTP status code.
func HTTPStatus(err error) int {
	switch {
	case errors.Is(err, ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrRateLimited):
		return http.StatusTooManyRequests
	default:
		return http.StatusBadRequest
	}
}

// JSONRPCCode maps a domain error to a JSON-RPC error code.
func JSONRPCCode(err error) int {
	switch {
	case errors.Is(err, ErrForbidden):
		return -32001
	case errors.Is(err, ErrNotFound):
		return -32002
	case errors.Is(err, ErrRateLimited):
		return -32003
	case errors.Is(err, ErrUnknownMethod):
		return -32601
	default:
		return -32000
	}
}
