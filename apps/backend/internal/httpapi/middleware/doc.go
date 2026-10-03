// Package middleware owns the Gin middleware that every Quack HTTP request
// passes through: trace propagation (RequestContext), the error envelope and
// panic containment (ErrorEnvelope, Recovery), request logging (Logger),
// browser hardening (SecurityHeaders, CORS, BodyLimit, CSRF), and the two
// authentication gates used by feature routes (RequireAuth, RequireGuildContext).
//
// Middleware here never renders feature data; it only decides whether a
// request may continue, stores the resolved session or guild context on the
// Gin context for handlers, and writes apierror envelopes when it stops a
// request. RequireAuth takes the narrow SessionStore port declared here;
// RequireGuildContext depends on internal/quack for the guild service and its
// sentinel errors, and everything reads cookie and limit settings from
// internal/config. Feature packages (routes, platform, modules) depend on this
// package, never the reverse.
package middleware
