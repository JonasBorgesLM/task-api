package link

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/JonasBorgesLM/cairn"
	"github.com/JonasBorgesLM/cairn/cairnhttp"

	"github.com/JonasBorgesLM/task-api/internal/middleware"
)

// maxRequestBodyBytes caps a decoded JSON request body, the same
// discipline every other package's Handler applies (see
// internal/task/handler.go's own constant).
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// maxListLimit bounds an explicit "limit" query parameter on GET
// /links, the same reasoning as task.maxTaskListLimit: a caller that
// passes one may not ask for an arbitrarily large page. Unlike task's
// GET /tasks, an absent limit here still defaults to a bounded page
// (defaultListLimit) rather than "everything" — cairn's own
// ListByOwner takes a required limit argument with no "unbounded"
// sentinel the way Repository.FindAll's limit < 0 is.
const maxListLimit = 100

// defaultListLimit is the page size GET /links uses when the caller
// does not pass ?limit=.
const defaultListLimit = 20

// linkService is the interface Handler depends on, so it can be tested
// with a fake implementation — the same shape every other package's
// Handler/*service interface takes.
type linkService interface {
	Create(ctx context.Context, ownerID string, req CreateRequest) (*cairn.Link, error)
	List(ctx context.Context, ownerID string, after cairn.Cursor, limit int) ([]*cairn.Link, cairn.Cursor, error)
	Revoke(ctx context.Context, ownerID string, code cairn.Code) error
}

// Handler exposes the link Service over HTTP: POST/GET /links and
// DELETE /links/{code}, all authenticated. The public, unauthenticated
// resolve route (GET/HEAD /{code}) is not part of this type — see
// NewPublicResolveHandler — because it is mounted outside /v1 on a
// different mux entirely (docs/DECISIONS.md's "Encurtador de links").
type Handler struct {
	svc           linkService
	logger        *slog.Logger
	publicBaseURL string
}

// NewHandler returns a new Handler. publicBaseURL (config.Config's
// LinkPublicBaseURL, no trailing slash) is joined with a created link's
// code to build the short_url field POST /links returns.
func NewHandler(svc linkService, logger *slog.Logger, publicBaseURL string) *Handler {
	return &Handler{svc: svc, logger: logger, publicBaseURL: publicBaseURL}
}

// RegisterRoutes registers the authenticated CRUD routes on v1, each
// wrapped with requireAuth. Call this on the same sub-mux every other
// domain package's RegisterRoutes uses (see cmd/api/main.go) — these
// three routes are part of the versioned /v1 contract, unlike the
// public resolve route.
func (h *Handler) RegisterRoutes(v1 *http.ServeMux, requireAuth middleware.Middleware) {
	protect := func(hf http.HandlerFunc) http.Handler { return requireAuth(hf) }

	v1.Handle("POST /links", protect(h.createLink))
	v1.Handle("GET /links", protect(h.listLinks))
	v1.Handle("DELETE /links/{code}", protect(h.revokeLink))
}

// NewPublicResolveHandler returns the http.Handler for the public,
// unauthenticated GET/HEAD /{code} route — mounted outside /v1, on the
// same outer mux as /health, never behind requireAuth or the CRUD
// routes above. Takes shortener directly rather than a Service: a
// read-only redirect has nothing to check ownership against (cairn's
// own Resolve already applies no ownership scoping, ADR-0010), so there
// is nothing here for Service's wrapper to add.
//
// The composition root registers this on the outer mux with a literal
// pattern such as "/{code}" — see cmd/api/main.go and
// docs/DECISIONS.md's "Encurtador de links" section for why it lives
// outside /v1 and how it avoids colliding with every other route.
func NewPublicResolveHandler(shortener *cairn.Shortener) http.Handler {
	return cairnhttp.NewHandler(shortener, cairnhttp.WithErrorEncoder(publicErrorEncoder))
}

// publicErrorEncoder is cairnhttp.DefaultErrorEncoder's own logic (SR-03:
// not-found, invalid, expired and revoked all collapse to the same 404;
// a store outage is 503, never a redirect) re-expressed in this
// project's {"error": "..."} envelope instead of cairnhttp's plain
// text — the only thing task-api's own ErrorEncoder needed to change.
// See docs/DECISIONS.md's "Encurtador de links" section for why this
// never opts into the 410-for-authenticated distinction
// docs/INTEGRATION.md's own example shows: nothing reaching this
// function is ever authenticated, by construction — this is the public
// route.
func publicErrorEncoder(w http.ResponseWriter, _ *http.Request, err error) {
	switch {
	case errors.Is(err, cairn.ErrInvalidCode), errors.Is(err, cairn.ErrCodeNotFound),
		errors.Is(err, cairn.ErrLinkExpired), errors.Is(err, cairn.ErrLinkRevoked):
		writeSimpleJSONError(w, http.StatusNotFound, "link not found")
	case errors.Is(err, cairn.ErrStoreUnavailable):
		writeSimpleJSONError(w, http.StatusServiceUnavailable, "link service unavailable")
	default:
		writeSimpleJSONError(w, http.StatusInternalServerError, "internal server error")
	}
}

func writeSimpleJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: message})
}

// createLinkRequest is the accepted body for POST /links.
type createLinkRequest struct {
	URL        string `json:"url"`
	VanityCode string `json:"vanity_code,omitempty"`
	// TTLSeconds is this link's time-to-live in seconds. Omitted or zero
	// means the Shortener's own configured default.
	TTLSeconds int64 `json:"ttl_seconds,omitempty"`
}

// linkResponse is the JSON representation of a *cairn.Link this API
// returns from POST /links, GET /links and — once printed — a caller's
// own record of what they created.
type linkResponse struct {
	Code      string     `json:"code"`
	ShortURL  string     `json:"short_url"`
	URL       string     `json:"url"`
	Vanity    bool       `json:"vanity"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// toLinkResponse reads link.Dest.Raw() rather than its redacted
// String()/LogValue() form — correct here specifically because this
// value goes back to the link's own owner in an authenticated response,
// not into a log line (see internal/link's own package doc and
// docs/DECISIONS.md's "Encurtador de links" § log audit for the
// boundary this crosses only in the direction it is allowed to).
func (h *Handler) toLinkResponse(link *cairn.Link) linkResponse {
	resp := linkResponse{
		Code:      string(link.Code),
		ShortURL:  h.publicBaseURL + "/" + string(link.Code),
		URL:       link.Dest.Raw(),
		Vanity:    link.Vanity,
		CreatedAt: link.CreatedAt,
	}
	if !link.ExpiresAt.IsZero() {
		expiresAt := link.ExpiresAt
		resp.ExpiresAt = &expiresAt
	}
	return resp
}

// createLink handles POST /links.
func (h *Handler) createLink(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	defer r.Body.Close()

	var req createLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}

	userID, _ := middleware.UserIDFromContext(r.Context())

	svcReq := CreateRequest{RawURL: req.URL, VanityCode: req.VanityCode}
	if req.TTLSeconds > 0 {
		svcReq.TTL = time.Duration(req.TTLSeconds) * time.Second
	}

	link, err := h.svc.Create(r.Context(), userID, svcReq)
	if err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	h.writeJSON(w, r, http.StatusCreated, h.toLinkResponse(link))
}

// listLinks handles GET /links. Accepts ?limit= (default
// defaultListLimit, capped at maxListLimit) and ?after= (an opaque
// cursor from a previous response's X-Next-Cursor header — see
// cairn.Cursor's own doc comment).
func (h *Handler) listLinks(w http.ResponseWriter, r *http.Request) {
	limit := defaultListLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			h.writeError(w, r, http.StatusBadRequest, "invalid input: limit must be a positive integer")
			return
		}
		if n > maxListLimit {
			h.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("invalid input: limit must be at most %d", maxListLimit))
			return
		}
		limit = n
	}
	after := cairn.Cursor(r.URL.Query().Get("after"))

	userID, _ := middleware.UserIDFromContext(r.Context())

	links, next, err := h.svc.List(r.Context(), userID, after, limit)
	if err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	resp := make([]linkResponse, len(links))
	for i, l := range links {
		resp[i] = h.toLinkResponse(l)
	}

	// A header, not a body envelope — the same choice X-Total-Count made
	// for GET /v1/tasks (docs/DECISIONS.md § "Total real na listagem")
	// and for the same reason: it keeps the response body a plain array,
	// so a caller that only ever reads the body sees no shape change.
	if next != "" {
		w.Header().Set("X-Next-Cursor", string(next))
	}

	h.writeJSON(w, r, http.StatusOK, resp)
}

// revokeLink handles DELETE /links/{code}.
func (h *Handler) revokeLink(w http.ResponseWriter, r *http.Request) {
	code := cairn.Code(r.PathValue("code"))
	userID, _ := middleware.UserIDFromContext(r.Context())

	if err := h.svc.Revoke(r.Context(), userID, code); err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleServiceError maps a Service error to an HTTP response. It is
// the only place in this package that does so.
//
// Every "this code did not resolve to something the caller may act on"
// condition — an unknown code, an invalid one, an expired or revoked
// one, and a wrong owner — collapses to the same 404, deliberately
// never distinguished even though every route reaching this function is
// authenticated. See ErrNotOwner's doc comment and Service.Revoke's own
// doc comment for why: Revoke's Resolve-first ownership check means a
// caller probing codes here learns nothing more than they would from
// the public resolve route, and docs/DECISIONS.md's "Encurtador de
// links" section records this as the reason the 410-for-authenticated
// opt-in docs/INTEGRATION.md's own example shows is not used by any
// route this package builds.
func (h *Handler) handleServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, cairn.ErrCodeNotFound), errors.Is(err, cairn.ErrInvalidCode),
		errors.Is(err, cairn.ErrLinkExpired), errors.Is(err, cairn.ErrLinkRevoked),
		errors.Is(err, ErrNotOwner):
		h.writeError(w, r, http.StatusNotFound, "link not found")
	case errors.Is(err, cairn.ErrDestinationRejected):
		reason, _ := cairn.RejectReasonFrom(err)
		h.writeErrorWithReason(w, r, http.StatusUnprocessableEntity, "destination rejected", string(reason))
	case errors.Is(err, cairn.ErrCodeExists):
		h.writeError(w, r, http.StatusConflict, "code already exists")
	case errors.Is(err, cairn.ErrVanityReserved):
		h.writeError(w, r, http.StatusUnprocessableEntity, "vanity code is reserved")
	case errors.Is(err, cairn.ErrVanityLength):
		h.writeError(w, r, http.StatusUnprocessableEntity, "vanity code length collides with the generated code length")
	case errors.Is(err, cairn.ErrCodeSpaceExhausted), errors.Is(err, cairn.ErrStoreUnavailable):
		h.writeError(w, r, http.StatusServiceUnavailable, "link service temporarily unavailable")
	default:
		requestID, _ := middleware.RequestIDFromContext(r.Context())
		h.logger.Error("unexpected service error",
			"error", err,
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.Path,
		)
		h.writeError(w, r, http.StatusInternalServerError, "internal server error")
	}
}

// writeJSON sets Content-Type, writes the status code and encodes data as JSON.
func (h *Handler) writeJSON(w http.ResponseWriter, r *http.Request, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		requestID, _ := middleware.RequestIDFromContext(r.Context())
		h.logger.Error("failed to encode response",
			"error", err,
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.Path,
		)
	}
}

// errorResponse is writeError/writeErrorWithReason's JSON body — the
// same shape every other package's Handler uses.
type errorResponse struct {
	Error  string `json:"error"`
	Reason string `json:"reason,omitempty"`
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, status int, message string) {
	h.writeJSON(w, r, status, errorResponse{Error: message})
}

func (h *Handler) writeErrorWithReason(w http.ResponseWriter, r *http.Request, status int, message, reason string) {
	h.writeJSON(w, r, status, errorResponse{Error: message, Reason: reason})
}
