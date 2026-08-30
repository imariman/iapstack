package app

import (
	"context"
	"errors"
)

// componentRun starts one blocking runtime component under a shared cancellation scope.
type componentRun func(context.Context) error

// runComponents runs every component, cancels siblings when one stops, and waits for all exits.
func runComponents(ctx context.Context, components ...componentRun) error {
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan error, len(components))
	for _, component := range components {
		go func(run componentRun) {
			results <- run(runContext)
		}(component)
	}

	errorsFound := make([]error, 0, len(components))
	for index := 0; index < len(components); index++ {
		err := <-results
		errorsFound = append(errorsFound, err)
		if index == 0 {
			cancel()
		}
	}
	return errors.Join(errorsFound...)
}
