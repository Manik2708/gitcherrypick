package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// Server assembles controllers into one listening HTTP server.
type Server struct {
	router   *chi.Mux
	resolver port.Middleware
	http     *http.Server
}

// readHeaderTimeout bounds how long a client may take to send its headers.
//
// Without it a handful of connections dribbling one byte at a time occupy the
// accept loop indefinitely.
const readHeaderTimeout = 10 * time.Second

// shutdownGrace is how long in-flight requests get to finish on shutdown.
const shutdownGrace = 15 * time.Second

// NewServer builds the router with the principal resolver already installed.
//
// The resolver is wired HERE rather than by each controller, because a
// controller that could choose its own middleware could choose to omit
// authentication (port.Middleware).
func NewServer(resolver port.Middleware) *Server {
	r := chi.NewRouter()
	r.Use(resolver.Wrap)

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusNotFound, service.CodeNotFound)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusMethodNotAllowed, service.CodeNotFound)
	})

	return &Server{router: r, resolver: resolver}
}

var _ port.Server = (*Server)(nil)

// Mount attaches each controller at the prefix it names.
//
// A controller reporting "/" is mounted as a set of top-level routes rather
// than a subtree, because chi cannot mount two handlers at the same root.
func (s *Server) Mount(controllers ...port.Controller) {
	for _, c := range controllers {
		prefix, handler := c.Routes()
		if prefix == "/" || prefix == "" {
			s.router.Mount("/", handler)
			continue
		}
		s.router.Mount(prefix, handler)
	}
}

// Health is the liveness endpoint. Unauthenticated by design: a load balancer
// has no credentials, and it reveals nothing.
func (s *Server) Health() {
	s.router.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}

// Handler exposes the router, for tests that drive it without a listener.
func (s *Server) Handler() http.Handler { return s.router }

// ListenAndServe binds and serves until Shutdown.
func (s *Server) ListenAndServe(addr string) error {
	s.http = &http.Server{
		Addr:              addr,
		Handler:           s.router,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serving on %s: %w", addr, err)
	}
	return nil
}

// Shutdown drains in-flight requests.
func (s *Server) Shutdown() error {
	if s.http == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()

	if err := s.http.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutting down: %w", err)
	}
	return nil
}

// PublicController serves the one surface a contributor may publish about
// themselves (ADR-0002).
//
// Separate from MeController because it is UNAUTHENTICATED: the token in the
// path is the entire credential, and it resolves to a scorecard or to nothing.
type PublicController struct {
	auth port.AuthService
}

// NewPublicController wires the share-link surface.
func NewPublicController(auth port.AuthService) *PublicController {
	return &PublicController{auth: auth}
}

var _ port.Controller = (*PublicController)(nil)

// Routes mounts /public.
func (c *PublicController) Routes() (string, http.Handler) {
	r := chi.NewRouter()
	r.Get("/scorecard/{token}", c.scorecard)
	return "/public", r
}

// scorecard resolves a published link.
//
// A revoked link is indistinguishable from one that never existed: revocation
// is immediate, and a distinguishable response would let anyone holding an old
// link confirm the contributor still exists.
func (c *PublicController) scorecard(w http.ResponseWriter, r *http.Request) {
	card, err := c.auth.PublicScorecard(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		if isNotFound(err) {
			// scorecard_not_found names what was looked for. It says nothing
			// about WHY — revoked and never-existed are the same answer, which
			// is what stops an old link confirming the contributor exists.
			writeCode(w, http.StatusNotFound, service.CodeScorecardNotFound)
			return
		}
		writeError(w, err)
		return
	}

	// identified=false: a published link proves nothing about who is reading
	// it, so it carries presentation and not a handle to query the contributor
	// by. Rank is absent for the same reason — it is a fact about the pool.
	writeJSON(w, http.StatusOK, scorecardOf(card, false))
}

// Principal is exposed for tests that need to assert what middleware resolved.
func Principal(ctx context.Context) (domain.Principal, bool) { return principalFrom(ctx) }
