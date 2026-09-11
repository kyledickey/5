// Package middleware owns the Gin middleware that every Quack HTTP request
// passes through: trace propagation (RequestContext), the error envelope and
// panic containment (ErrorEnvelope, Recovery), request logging (Logger),
// browser hardening (SecurityHeaders, CORS, BodyLimit, CSRF), and the two
// authentication gates used by feature routes (RequireAuth, RequireGuildContext).
//
// Middleware here never renders feature data; it only decides whether a
// request may continue, stores the resolved session or guild context on the
// Gin context for handlers, and writes apierror envelopes when it stops a
// request. It depends on internal/quack for the session repository, guild
// service, and sentinel errors, and on internal/config for cookie and limit
// settings; feature packages (routes, platform, modules) depend on it, never
// the reverse.
package middleware
