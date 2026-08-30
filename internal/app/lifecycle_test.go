package app

import (
	"context"
	"errors"
	"testing"
)

// TestRunComponentsCancelsSiblingsAndJoinsErrors verifies structured component shutdown.
func TestRunComponentsCancelsSiblingsAndJoinsErrors(t *testing.T) {
	t.Parallel()

	firstError := errors.New("api stopped")
	siblingStopped := make(chan struct{})
	err := runComponents(context.Background(),
		func(context.Context) error {
			return firstError
		},
		func(ctx context.Context) error {
			<-ctx.Done()
			close(siblingStopped)
			return nil
		},
	)

	if !errors.Is(err, firstError) {
		t.Fatalf("runComponents() error = %v, want first component error", err)
	}
	select {
	case <-siblingStopped:
	default:
		t.Fatal("runComponents() returned before sibling shutdown")
	}
}

// TestRunComponentsStopsAfterParentCancellation verifies parent cancellation reaches all components.
func TestRunComponentsStopsAfterParentCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 2)
	component := func(ctx context.Context) error {
		started <- struct{}{}
		<-ctx.Done()
		return nil
	}
	result := make(chan error, 1)
	go func() {
		result <- runComponents(ctx, component, component)
	}()

	<-started
	<-started
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("runComponents() error = %v, want nil", err)
	}
}
