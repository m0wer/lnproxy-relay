package nostr

import (
	"errors"
	"log"
	"strings"

	relay "github.com/lnproxy/lnproxy-relay"
)

// Server implements WrapHandler by driving a *relay.Relay. It checks feature
// negotiation and maps the relay's client-facing and internal errors to the
// base protocol error format.
type Server struct {
	Relay *relay.Relay
	// Offer is consulted for advertised features so unsupported wrap formats
	// are rejected before touching the node.
	Offer Offer
}

// NewServer constructs a Server.
func NewServer(r *relay.Relay, offer Offer) *Server {
	return &Server{Relay: r, Offer: offer}
}

// Wrap validates the requested output format, opens a circuit, and returns the
// proxy invoice or an error response.
func (s *Server) Wrap(req Request) Response {
	if req.Method != "" && req.Method != MethodWrap {
		return errorResponse("unsupported method")
	}
	feature := WrapFeature(req.Wrap)
	if !s.Offer.HasFeature(feature) {
		return errorResponse("unsupported wrap format: " + req.Wrap)
	}

	proxyInvoice, err := s.Relay.OpenCircuit(req.ProxyParameters())
	if err == nil {
		return Response{ProxyInvoice: proxyInvoice}
	}
	if isClientFacing(err) {
		return errorResponse(strings.TrimSpace(err.Error()))
	}
	log.Println("nostr: internal error for request:", err)
	return errorResponse("internal error")
}

// isClientFacing reports whether err is a relay client-facing error, whose
// message is safe to return to the requester.
func isClientFacing(err error) bool {
	// relay.ClientFacing is an empty-message sentinel joined into client-facing
	// errors; errors.Is matches it.
	return errors.Is(err, relay.ClientFacing)
}
