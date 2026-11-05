package telemetry

import (
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// instruments caches metric instruments against the meter provider that was
// current when they were created.
//
// The OpenTelemetry global providers are no-op until Init runs, so instruments
// must never be created in package-level variables — they would bind to the
// no-op meter forever. Instead they are resolved lazily through this cache,
// which rebuilds them whenever the provider changes.
type instruments[T any] struct {
	mu       sync.Mutex
	provider metric.MeterProvider
	scope    string
	build    func(metric.Meter) T
	value    T
}

func newInstruments[T any](scope string, build func(metric.Meter) T) *instruments[T] {
	return &instruments[T]{scope: scope, build: build}
}

// Get returns the instrument set for the current global meter provider.
func (i *instruments[T]) Get() T {
	provider := otel.GetMeterProvider()

	i.mu.Lock()
	defer i.mu.Unlock()

	if i.provider == nil || i.provider != provider {
		i.provider = provider
		i.value = i.build(provider.Meter(i.scope))
	}
	return i.value
}
