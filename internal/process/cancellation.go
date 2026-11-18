package process

import (
	"context"
	"syscall"
)

type signalKey struct{}

// WithCancellationSignal lets an executable preserve SIGINT versus SIGTERM
// while the orchestration core remains independent of global signal handlers.
func WithCancellationSignal(ctx context.Context, signal func() syscall.Signal) context.Context {
	return context.WithValue(ctx, signalKey{}, signal)
}
func cancellationSignal(ctx context.Context) syscall.Signal {
	if current, ok := ctx.Value(signalKey{}).(func() syscall.Signal); ok {
		if value := current(); value != 0 {
			return value
		}
	}
	return syscall.SIGTERM
}
