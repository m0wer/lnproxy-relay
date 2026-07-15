# lnproxy-relay

## Running a relay

This program uses the lnd REST API to handle lightning things so you'll need an lnd.conf with,
for example:

	restlisten=localhost:8080
To configure the relay follow the usage instructions:

	usage: ./lnproxy [flags] lnproxy.macaroon
	lnproxy.macaroon
		Path to lnproxy macaroon. Generate it with:
			lncli bakemacaroon --save_to lnproxy.macaroon
				uri:/lnrpc.Lightning/DecodePayReq \
				uri:/lnrpc.Lightning/LookupInvoice \
				uri:/invoicesrpc.Invoices/AddHoldInvoice \
				uri:/invoicesrpc.Invoices/SubscribeSingleInvoice \
				uri:/invoicesrpc.Invoices/CancelInvoice \
				uri:/invoicesrpc.Invoices/SettleInvoice \
				uri:/routerrpc.Router/SendPaymentV2 \
				uri:/routerrpc.Router/EstimateRouteFee \
				uri:/chainrpc.ChainKit/GetBestBlock
	-lnd string
		host for lnd's REST api (default "https://127.0.0.1:8080")
	-lnd-cert string
		lnd's self-signed cert (set to empty string for no-rest-tls=true) (default ".lnd/tls.cert")
	-port string
		http port over which to expose api (default "4747")

Run the binary:

	$ ./lnproxy-http-relay-openbsd-amd64-00000000 lnproxy.macaroon
	1970/01/01 00:00:00 HTTP server listening on: localhost:4747

and on a separate terminal, test with:

	curl -s --header "Content-Type: application/json" \
		--request POST \
		--data '{"invoice":"<bolt11 invoice>"}' \
		http://localhost:4747/spec

## Expose your relay over tor

If you know how to run a server you can put your relay behind a reverse proxy and and expose it to the internet.
A simpler route is to use tor.

Install tor, then edit `/etc/tor/torrc` to add:

	HiddenServiceDir /var/tor/lnproxy/
	HiddenServicePort 80 127.0.0.1:4747

and run:

	cat /var/tor/lnproxy.org/hostname

to get the onion url and try:

	torify curl -s --header "Content-Type: application/json" \
		--request POST \
		--data '{"invoice":"<bolt11 invoice>"}' \
		http://<your .onion url>/spec

HTTP clients can configure this endpoint explicitly. Nostr providers can also
advertise direct endpoints so clients discover them without an HTTP directory.

## Configuring your fees and limits

By default the relay charges a small base fee plus a proportional fee and
accepts invoices between 10 sats and 1,000,000 sats. Operators can set their own
fees and amount limits without recompiling, via flags or environment variables
(the flag wins when set, otherwise the environment variable, otherwise the
built-in default):

| flag | env var | meaning |
|---|---|---|
| `-min-msat` | `LNPROXY_MIN_MSAT` | minimum invoice amount (msat) |
| `-max-msat` | `LNPROXY_MAX_MSAT` | maximum invoice amount (msat) |
| `-base-fee-msat` | `LNPROXY_BASE_FEE_MSAT` | relay base fee (msat) |
| `-fee-ppm` | `LNPROXY_FEE_PPM` | relay proportional fee (ppm) |
| `-max-expiry` | `LNPROXY_MAX_EXPIRY` | maximum proxy invoice expiry (seconds) |

For example, to cap proxied amounts at 500,000 sats and charge 0.2%:

	./lnproxy-http-relay ... -max-msat 500000000 -fee-ppm 2000

## Advertising over nostr (decentralized discovery)

The `nostr-relay` binary advertises your relay on
[nostr](https://github.com/nostr-protocol/nips) so clients can discover it
without you having to get your URL added to a list, and serves wrap requests
over encrypted nostr messages. See the protocol in
[the spec](https://github.com/lnproxy/spec/blob/main/nostr.md).

It uses the same lnd setup as the HTTP relay, plus two extra macaroon
permissions for node attestation (omit them if you pass `-disable-ln-signing`):

	lncli bakemacaroon --save_to lnproxy.macaroon \
		uri:/lnrpc.Lightning/DecodePayReq \
		uri:/lnrpc.Lightning/LookupInvoice \
		uri:/lnrpc.Lightning/SignMessage \
		uri:/lnrpc.Lightning/GetInfo \
		uri:/invoicesrpc.Invoices/AddHoldInvoice \
		uri:/invoicesrpc.Invoices/SubscribeSingleInvoice \
		uri:/invoicesrpc.Invoices/CancelInvoice \
		uri:/invoicesrpc.Invoices/SettleInvoice \
		uri:/routerrpc.Router/SendPaymentV2 \
		uri:/routerrpc.Router/EstimateRouteFee \
		uri:/chainrpc.ChainKit/GetBestBlock

Run it:

	./nostr-relay -nostr-relays wss://nos.lol,wss://relay.damus.io lnproxy.macaroon

Or build the container image directly from this repository (no sibling `lnc`
checkout or parent-directory build context is required):

	docker build -t lnproxy-nostr-relay .

Useful flags (all fee/limit flags above also apply):

| flag | meaning |
|---|---|
| `-nostr-relays` (env `LNPROXY_NOSTR_RELAYS`) | comma-separated relay URLs (working defaults built in) |
| `-nostr-key` | path to the persistent identity key (created if absent) |
| `-network` (env `LNPROXY_NETWORK`) | `mainnet`/`testnet`/`signet`/`regtest`; validated at startup, offers are tagged with it so clients on other networks never see them |
| `-features` | advertised feature flags, e.g. `pay_bolt11,wrap_bolt11` |
| `-min-request-pow` | NIP-13 difficulty required from clients (DoS protection) |
| `-announce-pow` | NIP-13 difficulty mined into each offer |
| `-disable-ln-signing` | do not attest the nostr identity with your node key |
| `-identity-pow` | anonymous identity proof of work bits (used with `-disable-ln-signing`) |
| `-urls` | direct HTTP/onion `/spec` endpoints to advertise, in preference order |
| `-http-listen` (env `LNPROXY_HTTP_LISTEN`) | optional direct HTTP listen address, for example `127.0.0.1:4747` |

By default the relay attests its nostr identity with its lightning node key, so
clients can verify that the advertisement belongs to a real node. A standard
proxy invoice already reveals the relay's node id to the client, so this leaks
nothing new. If you only ever issue blinded or BOLT12 proxy invoices and want to
keep your node id private, run with `-disable-ln-signing` and optionally
`-identity-pow` instead.

To let discovered clients contact the provider directly before using nostr as a
fallback, run both transports in the same process:

	./nostr-relay \
		-nostr-relays wss://nos.lol,wss://relay.damus.io \
		-http-listen 127.0.0.1:4747 \
		-urls https://lnproxy.example.com/spec,http://<your-v3-address>.onion/spec \
		lnproxy.macaroon

When both `-http-listen` and `-urls` are set, the offer automatically advertises
`request_id_v1`. Direct and nostr retries then share one idempotency cache, so a
lost HTTP response cannot open a second hold invoice. Put clearnet listeners
behind an HTTPS reverse proxy. The direct HTTP endpoint does not have the Nostr
request proof-of-work gate, so public deployments should also enforce connection
and request rate limits at that proxy. Onion services can forward to the
loopback listener directly.

Note on privacy: as a relay operator you see the complete invoices you are asked
to pay, including their destination, amount and description/memo. A direct
clearnet client also exposes its IP unless it uses a proxy. Nostr transport hides
that IP from the provider only when the Nostr relay does not disclose or share
connection metadata. A direct onion endpoint over Tor avoids both third-party
relay metadata and disclosure of the client IP to the provider.

## Operating your relay

Sending `SIGINT` (with Ctrl-C) to the running relay will cause it to shutdown the http server
and stop accepting new invoices, it will wait for the last open invoice to expire, before fully shutting itself down.
A second `SIGINT` will cancel all open invoices and cause the relay to shutdown immediately.

When upgrading to the latest binaries, simply send one `SIGINT`
and allow the program to shut itself down gracefully.
It is safe to start the new binary immediately since the http server
from the first binary will already have shut itself down.
This way your relay can continue to proxy payments even while upgrading.

### Recovering from errors

If an unexpected error occurs when a payment to an original invoice is settled
but the accepted proxy invoice payment is not yet settled,
funds will be at risk.
This lnproxy relay tries, wherever possible, to completely shutdown in this situation:
if a single circuit does not complete as expected, the executable will
shutdown and stop accepting new invoices or sending out new payments to settle
active invoices.
This ensures that at most `MaxAmountMsat` Bitcoin will be in a "limbo" state
at any one time (the default value is 1,000,000 satoshis).

Even if such an error occurs, and an lnproxy relay circuit ends up in a limbo state,
it will almost certainly be possible to recover from the error manually.
If you notice that your relay excutable has terminated
(it's easy to set up an alert from this on *NIX systems by just adding
another command to follow the lnproxy relay command in whatever script invokes it),
you will have `CltvDeltaAlpha` blocks (by default about one day) to
manually settle the proxy payment.
To do this, simply use `lncli listinvoices` to find any invoices in the `ACCEPTED` state,
and then lookup their associated payments using the payment hash (`r_hash`).
If the payment was completed you should have a preimage you can use to
settle the `ACCEPTED` invoice.  If the payment failed, no funds are at risk,
you can cancel the hodl invoice.

## Development

Unit tests use a mocked lightning node, so they need no external services:

	go test ./...

The nostr transport also has an integration test that runs against a real
nostr relay in Docker, exercising the publish/subscribe, NIP-44 encryption and
NIP-13 proof-of-work paths over the wire:

	docker compose -f docker-compose.test.yml up -d
	LNPROXY_TEST_NOSTR_RELAY=ws://127.0.0.1:7777 go test -tags=integration ./nostr/...
	docker compose -f docker-compose.test.yml down -v

The integration test is skipped when `LNPROXY_TEST_NOSTR_RELAY` is unset.
