package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lnproxy/lnproxy-relay/nostr"
)

type recordingWrapper struct {
	request  nostr.Request
	response nostr.Response
}

type blockingWrapper struct {
	started chan struct{}
	release chan struct{}
}

func (w *blockingWrapper) Wrap(request nostr.Request) nostr.Response {
	close(w.started)
	<-w.release
	return nostr.Response{RequestID: request.RequestID, ProxyInvoice: "lnbc-proxy"}
}

func (w *recordingWrapper) Wrap(request nostr.Request) nostr.Response {
	w.request = request
	return w.response
}

func TestHandlerWrapsDirectRequest(t *testing.T) {
	requestID := strings.Repeat("a", 64)
	wrapper := &recordingWrapper{response: nostr.Response{RequestID: requestID, ProxyInvoice: "lnbc-proxy"}}
	body := bytes.NewBufferString(`{"method":"wrap","request_id":"` + requestID + `","invoice":"lnbc1...","wrap":"bolt11"}`)
	req := httptest.NewRequest(http.MethodPost, "/spec", body)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	NewHandler(wrapper).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if wrapper.request.RequestID != requestID || wrapper.request.Invoice != "lnbc1..." {
		t.Fatalf("unexpected request: %+v", wrapper.request)
	}
	var response nostr.Response
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.RequestID != requestID || response.ProxyInvoice != "lnbc-proxy" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestHandlerAnswersCORSPreflight(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/spec", nil)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "X-Requested-With, Content-Type")
	recorder := httptest.NewRecorder()

	NewHandler(&recordingWrapper{}).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodPost) {
		t.Fatalf("Access-Control-Allow-Methods = %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "X-Requested-With") {
		t.Fatalf("Access-Control-Allow-Headers = %q", got)
	}
}

func TestHandlerRejectsMalformedAndOversizedBodies(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed", body: "{"},
		{name: "multiple objects", body: `{}` + `{}`},
		{name: "oversized", body: `{"invoice":"` + strings.Repeat("x", maxRequestBody) + `"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/spec", strings.NewReader(test.body))
			NewHandler(&recordingWrapper{}).ServeHTTP(recorder, req)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestHandlerRejectsOtherMethods(t *testing.T) {
	recorder := httptest.NewRecorder()
	NewHandler(&recordingWrapper{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/spec", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandlerCanRequireRequestID(t *testing.T) {
	wrapper := &recordingWrapper{}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/spec", strings.NewReader(`{"invoice":"lnbc1..."}`))
	NewHandlerWithOptions(wrapper, Options{RequireRequestID: true}).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var response nostr.Response
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "ERROR" || response.Reason != "request_id required" {
		t.Fatalf("unexpected response: %+v", response)
	}
	if wrapper.request.Invoice != "" {
		t.Fatal("wrapper was called without a request ID")
	}
}

func TestHandlerBoundsConcurrentRequests(t *testing.T) {
	wrapper := &blockingWrapper{started: make(chan struct{}), release: make(chan struct{})}
	handler := NewHandlerWithOptions(wrapper, Options{MaxConcurrent: 1})
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/spec", strings.NewReader(`{"invoice":"first"}`)))
	}()
	<-wrapper.started

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/spec", strings.NewReader(`{"invoice":"second"}`)))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	close(wrapper.release)
	<-firstDone
}
