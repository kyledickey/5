// Package interactions receives every InteractionCreate from the gateway and
// routes it to the ui.Handler registered for its command name (via CommandLookup)
// or component/modal custom ID (via ComponentRegistry). It owns the response
// lifecycle after the handler returns: it sends the immediate response or
// acknowledgement, runs the async Task on its own goroutine, and reports task
// failures back to the user. It also deduplicates Discord's redelivered
// interactions (InteractionDeduper) and recovers from handler panics so one bad
// interaction cannot crash the process.
//
// Error routing: an immediate response that Discord rejects is only logged, with
// numeric diagnostics and never tokens or bodies. A Task error is delivered
// privately to the invoking user. When the acknowledgement was a public defer
// that nobody has edited yet, the placeholder is deleted first so the channel
// never shows a stale "thinking" message or an error; when the task already
// published a result it is left untouched. In DMs ephemeral flags are stripped
// because Discord rejects them there.
//
// The package imports ui and quack but never commands or views: handlers are
// opaque functions to the dispatcher.
package interactions
