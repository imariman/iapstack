package stores

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/imariman/iapstack/internal/core"
)

// ErrAdapterNotFound indicates that no adapter is registered for a provider.
var ErrAdapterNotFound = errors.New("store adapter not found")

type Registry struct {
	adapters map[core.Provider]Adapter
}

func NewRegistry(adapters ...Adapter) (*Registry, error) {
	registry := &Registry{adapters: make(map[core.Provider]Adapter, len(adapters))}
	for _, adapter := range adapters {
		if adapter == nil || isNilAdapter(adapter) {
			return nil, errors.New("store adapter is nil")
		}
		provider := adapter.Provider()
		if err := provider.Validate(); err != nil {
			return nil, err
		}
		if _, duplicate := registry.adapters[provider]; duplicate {
			return nil, fmt.Errorf("duplicate store adapter for provider %q", provider)
		}
		registry.adapters[provider] = adapter
	}
	return registry, nil
}

func (registry *Registry) Adapter(provider core.Provider) (Adapter, error) {
	if err := provider.Validate(); err != nil {
		return nil, err
	}
	if registry == nil {
		return nil, fmt.Errorf("%w: %s", ErrAdapterNotFound, provider)
	}
	adapter, found := registry.adapters[provider]
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrAdapterNotFound, provider)
	}
	return adapter, nil
}

func isNilAdapter(adapter Adapter) bool {
	value := reflect.ValueOf(adapter)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
