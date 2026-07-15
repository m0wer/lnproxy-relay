package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/lnproxy/lnproxy-relay/nostr"
)

const maxRequestBody = 64 << 10

// Options controls direct HTTP admission policy.
type Options struct {
	RequireRequestID bool
	MaxConcurrent    int
}

// Wrapper processes the transport-neutral wrap request used by HTTP and nostr.
type Wrapper interface {
	Wrap(nostr.Request) nostr.Response
}

// NewHandler returns an HTTP handler serving the direct wrap endpoint at /spec.
func NewHandler(wrapper Wrapper) http.Handler {
	return NewHandlerWithOptions(wrapper, Options{MaxConcurrent: 32})
}

// NewHandlerWithOptions returns a direct HTTP handler with explicit admission
// and request-ID policy.
func NewHandlerWithOptions(wrapper Wrapper, options Options) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/spec", specHandler(wrapper, options))
	return mux
}

func specHandler(wrapper Wrapper, options Options) http.Handler {
	var admission chan struct{}
	if options.MaxConcurrent > 0 {
		admission = make(chan struct{}, options.MaxConcurrent)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Origin, X-Requested-With, Content-Type, Accept")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Vary", "Access-Control-Request-Headers")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST, OPTIONS")
			writeJSON(w, http.StatusMethodNotAllowed, nostr.Response{Status: "ERROR", Reason: "method not allowed"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		decoder := json.NewDecoder(r.Body)
		var request nostr.Request
		if err := decoder.Decode(&request); err != nil {
			writeJSON(w, http.StatusBadRequest, nostr.Response{Status: "ERROR", Reason: "bad request"})
			return
		}
		if err := ensureEOF(decoder); err != nil {
			writeJSON(w, http.StatusBadRequest, nostr.Response{Status: "ERROR", Reason: "bad request"})
			return
		}
		if options.RequireRequestID && request.RequestID == "" {
			writeJSON(w, http.StatusOK, nostr.Response{Status: "ERROR", Reason: "request_id required"})
			return
		}
		if admission != nil {
			select {
			case admission <- struct{}{}:
				defer func() { <-admission }()
			default:
				writeJSON(w, http.StatusServiceUnavailable, nostr.Response{Status: "ERROR", Reason: "server busy"})
				return
			}
		}

		writeJSON(w, http.StatusOK, wrapper.Wrap(request))
	})
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("multiple JSON values")
}

func writeJSON(w http.ResponseWriter, status int, response nostr.Response) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}
