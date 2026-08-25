// Package validation classifies trusted-boundary input failures without matching error text.
package validation

import "errors"

// invalidError retains the original cause while exposing a stable classification sentinel.
type invalidError struct {
	cause error
}

var (
	// ErrInvalid identifies data rejected before operational side effects begin.
	ErrInvalid = errors.New("invalid input")
)

// Wrap marks one input failure as invalid while preserving its original error chain.
func Wrap(err error) error {
	if err == nil || errors.Is(err, ErrInvalid) {
		return err
	}
	return &invalidError{cause: err}
}

// Error returns the underlying validation detail for internal diagnostics.
func (err *invalidError) Error() string {
	return err.cause.Error()
}

// Is exposes the stable invalid-input classification.
func (err *invalidError) Is(target error) bool {
	return target == ErrInvalid
}

// Unwrap preserves typed causes needed by internal callers.
func (err *invalidError) Unwrap() error {
	return err.cause
}
