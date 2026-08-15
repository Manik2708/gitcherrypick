package port

import "net/http"

// Controllers.
//
// A controller only serializes, deserializes and validates IO. No business
// logic (CLAUDE.md). It extracts a domain.Principal from the request context
// and passes it to a service, which decides access — a controller that decided
// access itself would put the rule in as many places as there are handlers.
//
// The interface is deliberately thin: one method that mounts routes. Exposing
// each handler as a named method would let a test call a handler directly and
// skip the middleware that resolves the principal, which is precisely the seam
// that must not be skippable.

// Controller mounts a group of routes onto a router.
//
// The router is http.Handler-shaped rather than chi-shaped so that this package
// does not import a web framework. ADR-0006 picks chi; nothing above the
// adapter layer needs to know.
type Controller interface {
	// Routes returns the handler for this controller's subtree, and the prefix
	// it should be mounted at.
	Routes() (prefix string, handler http.Handler)
}

// Middleware wraps a handler. Ordering is fixed by the server, not by a
// controller — a controller that could reorder authentication could remove it.
type Middleware interface {
	Wrap(next http.Handler) http.Handler
}

// PrincipalResolver turns a request's credentials into a principal and puts it
// in the context.
//
// This is the single place a token becomes an identity. There is no test-only
// header and no bypass: the e2e suite signs in through the real endpoints and
// carries a real bearer token, because an authentication bypass compiled into
// the shipping binary is one build-tag mistake away from production.
type PrincipalResolver interface {
	Middleware
}

// Server assembles controllers into a listening HTTP server.
type Server interface {
	Mount(controllers ...Controller)
	ListenAndServe(addr string) error
	Shutdown() error
}

// ControlPlane is the fake-control listener used by the integration suite.
//
// It binds a SEPARATE socket and exists only when the binary is started with
// --adapters=fake. Production binds no control listener at all, so there is no
// route to reach — not a route that checks a flag and refuses. That distinction
// is the reason this is a second listener rather than a path on the main one.
type ControlPlane interface {
	// PrimeFake pushes adapter state — canned GitHub responses, AI judgements,
	// a clock setting — into the running process before a step's request.
	PrimeFake(kind string, payload []byte) error

	// RunAction drives a background job synchronously, so a fixture can assert
	// on its effects without polling. The alternative is a sleep, and a sleep
	// is a flake with a timer attached.
	RunAction(name string, payload []byte) ([]byte, error)

	ListenAndServe(addr string) error
	Shutdown() error
}
