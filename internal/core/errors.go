package core

import (
	"errors"
	"fmt"
)

// ErrorKind is the retry classification of docs/05 §7.
type ErrorKind int

const (
	KindPermanent  ErrorKind = iota // 404, malformed input, invalid credential
	KindTransient                   // timeout, reset, 5xx, FLOOD_WAIT
	KindCapability                  // missing/outdated sidecar -> degrade
	KindSecurity                    // SSRF, path traversal, checksum mismatch
)

func (k ErrorKind) String() string {
	switch k {
	case KindPermanent:
		return "permanent"
	case KindTransient:
		return "transient"
	case KindCapability:
		return "capability"
	case KindSecurity:
		return "security"
	default:
		return "unknown"
	}
}

// Error codes are the single source of truth for docs/07 §6.
const (
	E_INVALID_INPUT          = "E_INVALID_INPUT"
	E_RESOLVE_FAILED         = "E_RESOLVE_FAILED"
	E_RESOLVE_SSRF_BLOCKED   = "E_RESOLVE_SSRF_BLOCKED"
	E_CAPABILITY_UNAVAILABLE = "E_CAPABILITY_UNAVAILABLE"
	E_TASK_NOT_FOUND         = "E_TASK_NOT_FOUND"
	E_ILLEGAL_TRANSITION     = "E_ILLEGAL_TRANSITION"
	E_STORAGE_FULL           = "E_STORAGE_FULL"
	E_CHECKSUM_MISMATCH      = "E_CHECKSUM_MISMATCH"
	E_TRANSIENT_NETWORK      = "E_TRANSIENT_NETWORK"
	E_FLOOD_WAIT             = "E_FLOOD_WAIT"
	E_POSTPROCESS_FAILED     = "E_POSTPROCESS_FAILED"
	E_INTERNAL               = "E_INTERNAL"
)

type errorSpec struct {
	status    int
	retryable bool
	kind      ErrorKind
}

var errorSpecs = map[string]errorSpec{
	E_INVALID_INPUT:          {400, false, KindPermanent},
	E_RESOLVE_FAILED:         {422, false, KindPermanent},
	E_RESOLVE_SSRF_BLOCKED:   {403, false, KindSecurity},
	E_CAPABILITY_UNAVAILABLE: {503, false, KindCapability},
	E_TASK_NOT_FOUND:         {404, false, KindPermanent},
	E_ILLEGAL_TRANSITION:     {409, false, KindPermanent},
	E_STORAGE_FULL:           {507, false, KindPermanent},
	E_CHECKSUM_MISMATCH:      {422, true, KindSecurity},
	E_TRANSIENT_NETWORK:      {502, true, KindTransient},
	E_FLOOD_WAIT:             {429, true, KindTransient},
	E_POSTPROCESS_FAILED:     {500, true, KindTransient},
	E_INTERNAL:               {500, false, KindPermanent},
}

// Error is both the internal error and the wire format of docs/07 §1.
type Error struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
	Kind      ErrorKind      `json:"-"`
	Status    int            `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Code + ": " + e.Message
}

// WithDetail attaches non-sensitive context (never credentials or raw URLs).
func (e *Error) WithDetail(key string, value any) *Error {
	if e.Details == nil {
		e.Details = make(map[string]any)
	}
	e.Details[key] = value
	return e
}

// HTTPStatus returns the status code for the REST layer.
func (e *Error) HTTPStatus() int {
	if e.Status != 0 {
		return e.Status
	}
	return 500
}

// Err builds a classified error by code.
func Err(code string, format string, args ...any) *Error {
	spec, ok := errorSpecs[code]
	if !ok {
		spec = errorSpecs[E_INTERNAL]
		code = E_INTERNAL
	}
	return &Error{
		Code:      code,
		Message:   fmt.Sprintf(format, args...),
		Retryable: spec.retryable,
		Kind:      spec.kind,
		Status:    spec.status,
	}
}

// AsError converts any error into the unified wire error.
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Err(E_INTERNAL, "%s", err.Error())
}

// IsRetryable reports whether the caller should back off and retry.
func IsRetryable(err error) bool {
	e := AsError(err)
	return e != nil && e.Retryable
}
