// Package apierror owns the one JSON error envelope every Quack HTTP response
// uses: {"error": {code, message, request_id, correlation_id}}. Handlers and
// middleware call Write to abort a request with a stable machine-readable Code
// and a message that is safe to show to clients; Default supplies the fallback
// code and message for a bare status when a handler produced an unstructured
// failure.
//
// The package imports only gin and internal/quack/idutil (for trace IDs). It
// must not import the application core so that any HTTP layer can use it
// without creating a dependency cycle.
package apierror
