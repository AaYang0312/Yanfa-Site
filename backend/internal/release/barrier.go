//go:build !integration

package release

import "context"

// Barrier is compiled to a no-op in production; no control environment is read.
func Barrier(context.Context, string) error { return nil }
