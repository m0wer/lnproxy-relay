package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lnproxy/lnc"
)

// nodeSigner is the narrow LND identity-key surface needed for nostr offer
// attestation. Keeping it local avoids requiring unpublished lnc extensions.
type nodeSigner interface {
	IdentityPubkey() (string, error)
	SignMessage([]byte) (string, error)
}

type lndSigner struct {
	lnd     *lnc.Lnd
	timeout time.Duration
}

func newLNDSigner(lnd *lnc.Lnd) *lndSigner {
	return &lndSigner{lnd: lnd, timeout: 15 * time.Second}
}

func (s *lndSigner) IdentityPubkey() (string, error) {
	req, err := s.request(http.MethodGet, "v1/getinfo", nil)
	if err != nil {
		return "", err
	}

	response := struct {
		IdentityPubkey string `json:"identity_pubkey"`
	}{}
	if err := s.do(req, &response); err != nil {
		return "", err
	}
	if response.IdentityPubkey == "" {
		return "", errors.New("v1/getinfo: empty identity_pubkey")
	}
	return response.IdentityPubkey, nil
}

func (s *lndSigner) SignMessage(message []byte) (string, error) {
	body, err := json.Marshal(struct {
		Message []byte `json:"msg"`
	}{Message: message})
	if err != nil {
		return "", err
	}
	req, err := s.request(http.MethodPost, "v1/signmessage", bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	response := struct {
		Signature string `json:"signature"`
	}{}
	if err := s.do(req, &response); err != nil {
		return "", err
	}
	if response.Signature == "" {
		return "", errors.New("v1/signmessage: empty signature")
	}
	return response.Signature, nil
}

func (s *lndSigner) request(method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, s.lnd.Host.JoinPath(path).String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Grpc-Metadata-macaroon", s.lnd.Macaroon)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (s *lndSigner) do(req *http.Request, response any) error {
	ctx := req.Context()
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}
	req = req.WithContext(ctx)
	resp, err := s.lnd.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if readErr != nil {
			return readErr
		}
		return fmt.Errorf("%s: HTTP %d: %s", req.URL.Path, resp.StatusCode, string(body))
	}
	if err := json.NewDecoder(resp.Body).Decode(response); err != nil && err != io.EOF {
		return err
	}
	return nil
}
