// Package idutil owns identifier generation and request-trace propagation for
// every transport in Quack: ULIDs for durable records and the request/correlation
// IDs carried through context by HTTP, Discord, and worker code.
//
// It depends only on the standard library and the ULID library. The domain
// package (internal/quack) re-exports the trace helpers for convenience, but
// packages that only need identifiers or traces should import idutil directly
// so that they do not pull the whole application core into their dependency
// graph (internal/httpapi/apierror and internal/logging rely on this).
package idutil
