package validation_test

import (
	"errors"
	"testing"

	"github.com/imariman/iapstack/internal/validation"
)

// TestWrapClassifiesAndPreservesCauses verifies typed invalid input does not depend on message text.
func TestWrapClassifiesAndPreservesCauses(t *testing.T) {
	t.Parallel()

	cause := errors.New("opaque failure detail")
	wrapped := validation.Wrap(cause)
	if !errors.Is(wrapped, validation.ErrInvalid) || !errors.Is(wrapped, cause) {
		t.Fatalf("Wrap() error chain = %v", wrapped)
	}
	if validation.Wrap(wrapped) != wrapped {
		t.Fatal("Wrap() did not preserve an existing classification")
	}
	if validation.Wrap(nil) != nil {
		t.Fatal("Wrap(nil) did not return nil")
	}
}
