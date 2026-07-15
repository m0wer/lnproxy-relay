package relay

import (
	"testing"
	"time"

	"github.com/lnproxy/lnc"
)

// fakeLN is a minimal lnc.LN for exercising wrap() feature-flag handling.
type fakeLN struct {
	decoded *lnc.DecodedInvoice
}

func (f *fakeLN) DecodeInvoice(string) (*lnc.DecodedInvoice, error) { return f.decoded, nil }
func (f *fakeLN) AddInvoice(lnc.InvoiceParameters) (string, error)  { return "lnbc-proxy", nil }
func (f *fakeLN) WatchInvoice([]byte) (*lnc.InvoiceState, error) {
	return &lnc.InvoiceState{State: lnc.Canceled}, nil
}
func (f *fakeLN) CancelInvoice([]byte) error { return nil }
func (f *fakeLN) PayInvoice(lnc.PaymentParameters) ([]byte, error) {
	return nil, lnc.PaymentFailed
}
func (f *fakeLN) SettleInvoice([]byte) error { return nil }
func (f *fakeLN) EstimateRoutingFee(lnc.DecodedInvoice, uint64) (uint64, uint64, error) {
	return 1000, 144, nil
}

func decodedWithFeatures(features ...string) *lnc.DecodedInvoice {
	hash := "0001020304050607080910111213141516171819202122232425262728293031"
	d := &lnc.DecodedInvoice{
		PaymentHash: hash,
		Timestamp:   uint64(time.Now().Unix()),
		Expiry:      3600,
		Description: "test",
		NumMsat:     1_000_000,
		CltvExpiry:  40,
		Destination: "02deadbeef",
	}
	d.Features = map[string]struct {
		Name       string `json:"name"`
		IsRequired bool   `json:"is_required"`
		IsKnown    bool   `json:"is_known"`
	}{}
	for _, f := range features {
		d.Features[f] = struct {
			Name       string `json:"name"`
			IsRequired bool   `json:"is_required"`
			IsKnown    bool   `json:"is_known"`
		}{Name: f}
	}
	return d
}

// TestWrapAcceptsBlindedPathFeatures guards the fix for LND 0.18 invoices, which
// set bolt11 blinded-path feature bit 263. Both 262 and 263 must be accepted.
func TestWrapAcceptsBlindedPathFeatures(t *testing.T) {
	for _, feat := range []string{"262", "263"} {
		t.Run("feature_"+feat, func(t *testing.T) {
			r := NewRelay(&fakeLN{decoded: decodedWithFeatures("8", "14", "17", "25", feat)})
			if _, _, err := r.wrap(ProxyParameters{Invoice: "lnbcrt1..."}); err != nil {
				t.Fatalf("wrap rejected feature %s: %v", feat, err)
			}
		})
	}
}

func TestWrapRejectsUnknownFeature(t *testing.T) {
	r := NewRelay(&fakeLN{decoded: decodedWithFeatures("8", "999")})
	_, _, err := r.wrap(ProxyParameters{Invoice: "lnbcrt1..."})
	if err == nil {
		t.Fatal("expected unknown feature flag to be rejected")
	}
}

// recordingLN drives a full circuit: WatchInvoice returns an Accepted state with
// a configurable CLTV delta, and it records whether the relay paid out or
// canceled. PayInvoice succeeds with a preimage.
type recordingLN struct {
	decoded      *lnc.DecodedInvoice
	cltvDelta    uint64
	paid         bool
	canceled     bool
	settled      bool
	gotCltvLimit uint64
}

func (m *recordingLN) DecodeInvoice(string) (*lnc.DecodedInvoice, error) { return m.decoded, nil }
func (m *recordingLN) AddInvoice(lnc.InvoiceParameters) (string, error)  { return "lnbc-proxy", nil }
func (m *recordingLN) WatchInvoice([]byte) (*lnc.InvoiceState, error) {
	return &lnc.InvoiceState{State: lnc.Accepted, CltvExpiryDelta: m.cltvDelta}, nil
}
func (m *recordingLN) CancelInvoice([]byte) error { m.canceled = true; return nil }
func (m *recordingLN) PayInvoice(p lnc.PaymentParameters) ([]byte, error) {
	m.paid = true
	m.gotCltvLimit = p.CltvLimit
	return []byte("preimage-bytes-32-aaaaaaaaaaaaaa"), nil
}
func (m *recordingLN) SettleInvoice([]byte) error { m.settled = true; return nil }
func (m *recordingLN) EstimateRoutingFee(lnc.DecodedInvoice, uint64) (uint64, uint64, error) {
	return 1000, 144, nil
}

// TestCircuitSwitchCancelsOnShortCltv guards against the uint64 underflow in the
// CltvLimit computation: when the accepted HTLC's CLTV delta is not larger than
// CltvDeltaAlpha, the relay must cancel rather than pay out with an unbounded
// CltvLimit (which would risk funds).
func TestCircuitSwitchCancelsOnShortCltv(t *testing.T) {
	r := NewRelay(nil)
	m := &recordingLN{cltvDelta: r.CltvDeltaAlpha} // exactly equal -> unsafe
	r.LN = m
	r.WaitGroup.Add(1)
	r.circuitSwitch([]byte("hash"), "lnbc1...", 1000)
	if m.paid {
		t.Fatal("relay paid out with an unsafe CLTV margin")
	}
	if !m.canceled {
		t.Fatal("relay did not cancel the unsafe invoice")
	}
}

// TestCircuitSwitchPaysWithSafeMargin verifies the happy path: a healthy CLTV
// delta yields CltvLimit = delta - CltvDeltaAlpha and the invoice is settled.
func TestCircuitSwitchPaysWithSafeMargin(t *testing.T) {
	r := NewRelay(nil)
	m := &recordingLN{cltvDelta: 500}
	r.LN = m
	r.WaitGroup.Add(1)
	r.circuitSwitch([]byte("hash"), "lnbc1...", 1000)
	if !m.paid {
		t.Fatal("relay did not pay out with a safe CLTV margin")
	}
	if m.gotCltvLimit != 500-r.CltvDeltaAlpha {
		t.Fatalf("CltvLimit = %d, want %d", m.gotCltvLimit, 500-r.CltvDeltaAlpha)
	}
	if !m.settled {
		t.Fatal("relay did not settle after learning the preimage")
	}
}
