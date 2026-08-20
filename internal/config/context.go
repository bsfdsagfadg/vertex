package config

import "context"

type snapshotContextKey struct{}

// WithSnapshot captures one immutable config/model view for all downstream
// work spawned from ctx.
func WithSnapshot(ctx context.Context, provider ConfigProvider) context.Context {
	if existing, ok := ctx.Value(snapshotContextKey{}).(ConfigProvider); ok && existing != nil {
		return ctx
	}
	return context.WithValue(ctx, snapshotContextKey{}, Snapshot(provider))
}

// FromContext returns the request snapshot, or the supplied fallback when no
// snapshot has been installed yet.
func FromContext(ctx context.Context, fallback ConfigProvider) ConfigProvider {
	if ctx != nil {
		if provider, ok := ctx.Value(snapshotContextKey{}).(ConfigProvider); ok && provider != nil {
			return provider
		}
	}
	return fallback
}
