# arca (arca-gateway)

Tramo's own client for ARCA web services and the `arca-gateway` process
that exposes it to core over gRPC (`proto/arca/v1`). Owned by backend. No
AfipSDK or any intermediary: the certificate and its key never leave this
process. Decisions in ADR 0004.

## What it does

| gRPC (ArcaService) | ARCA service | Notes |
|--------------------|--------------|-------|
| `GetPersona(cuit)` | `ws_sr_constancia_inscripcion` `getPersona_v2` | Name, type, key status, fiscal address, taxes, monotributo category. `condicion_iva` is derived only from active taxes (30 IVA, 20 monotributo); anything else is `NO_DETERMINADA`, never guessed. |
| `ConstatarComprobante(...)` | `wscdc` `ComprobanteConstatar` | APROBADO (A) or RECHAZADO (R) with ARCA's observations and errors. Amount travels as integer cents. |

Both are queries with **Tramo's own CUIT and certificate**. They prove a CUIT
exists or a voucher was authorized. They do **not** prove that a client
delegated anything to Tramo (that needs a WSFE call on the client's CUIT,
out of scope for now).

gRPC errors: `INVALID_ARGUMENT` (bad input, nothing sent to ARCA),
`NOT_FOUND` (no person with that CUIT), `UNAVAILABLE` (ARCA or WSAA did not
answer, refused a ticket, or the gateway is waiting before asking again),
`INTERNAL`. Health: standard `grpc.health.v1`.

## WSAA tickets

TRA built with `generationTime` 5 minutes back and `expirationTime` 10
minutes ahead, in GMT-3; signed as CMS SignedData with the content attached
(SHA-256, RSA); `loginCms` returns a TA valid 12 hours. One TA per service,
shared by every caller, renewed when less than 10 minutes remain; at most one
`loginCms` per service in flight. After any WSAA error the gateway waits 60
seconds before asking again (WSAA spec), and keeps using a TA that is still
valid if renewal is refused with `coe.alreadyAuthenticated`.

**Known limit:** TAs live in memory. If the gateway restarts while ARCA
still considers a TA valid, WSAA answers `coe.alreadyAuthenticated` and that
service is unavailable until the old TA expires (up to 12 hours). See ADR 0004.

## Configuration (environment only, read only by cmd/arca-gateway)

| Variable | Meaning |
|----------|---------|
| `ARCA_ENV` | `sim` (default), `homologacion`, `produccion` |
| `ARCA_CUIT` | Tramo's CUIT; must match the certificate `serialNumber` (`CUIT n`) |
| `ARCA_CERT` | certificate, PEM text |
| `ARCA_KEY` | private key, PEM text (PKCS#8 or PKCS#1); must match the certificate |
| `ARCA_WSAA_URL`, `ARCA_CONSTANCIA_URL`, `ARCA_WSCDC_URL` | optional overrides; https only |
| `ARCA_TIMEOUT_SECONDS` | per call, default 30 |
| `GRPC_ADDR` | listen address, default `:9090` |

Endpoints (from the manuals in `eng/arca/manuales`): WSAA
`wsaahomo.afip.gov.ar` / `wsaa.afip.gov.ar`; constancia
`awshomo.arca.gob.ar` / `aws.arca.gob.ar` (`/sr-padron/webservices/personaServiceA5`);
WSCDC `wswhomo.afip.gob.ar` / `servicios1.arca.gob.ar` (`/WSCDC/service.asmx`).

The gateway does not load core's config: it never sees `DATABASE_URL`,
`MASTER_KEY` or `JWT_SIGNING_KEY`.

## Simulator (`ARCA_ENV=sim`)

`sim` starts in process on loopback with a throwaway CA and certificate. It
verifies the CMS signature against that CA, checks TRA times and service,
issues 12 hour TAs, and refuses a second TA for the same service while one
is valid, like WSAA. Fixture data (all fictitious, valid check digits):

| CUIT | What it is |
|------|------------|
| 30000000015 | sociedad, IVA active: `RESPONSABLE_INSCRIPTO` |
| 20000000060 | persona humana, monotributo |
| 27000000006 | persona humana, only income tax: `NO_DETERMINADA` |
| 20000000028 | valid digit, not registered: `NOT_FOUND` |

The sim gateway itself acts as CUIT 30000000023. Every CUIT above is
synthetic (all zeros pattern). The only CUITs taken from elsewhere are the
ones inside ARCA's own manual examples (`testdata/`, `manual_test.go`) and
WSAA's own CUIT in the simulated ticket `source`, each cited where it appears.

One authorized voucher: CAE `76123456789012`, issuer 30000000015, point of
sale 1, type 1, number 2, date 20261103, total 12100050 cents.

The simulator's fault codes for bad tokens and its WSCDC observation codes
(9001 to 9105) are its own, not ARCA's.

## Tables, jobs, events

None. The gateway is stateless apart from the in-memory TA cache.

## Sensitive data

- The private key: only in `ARCA_KEY`, only in this process
  (`TestOnlyTheGatewayReadsTheKey` fails if any other package mentions it).
  Never logged; config errors never echo it.
- TA token and sign: in memory only, never logged or returned.
- CUITs and voucher data pass through; nothing is stored.
- gRPC between core and gateway is plaintext inside the compose network and
  has no authentication yet (port not published). See ADR 0004.

## How it is tested

`go test ./internal/arca/...` (no database, no network beyond loopback).

- `manual_test.go`: the example responses printed in ARCA's manuals
  (`testdata/`, copied verbatim) decode through the same path as live calls:
  constancia v4.1 and WSCDC; a WSAA fault comes back typed.
- `wsaa_test.go`, against the simulator: the TRA carries GMT-3 times and is
  a CMS that verifies; 40 concurrent calls make exactly one `loginCms` per
  service; after a restart `coe.alreadyAuthenticated` surfaces and the next
  call inside 60 seconds does not hit WSAA; a certificate from another CA is
  refused; IVA condition for the three fixtures and not found; a voucher off
  by one cent is rejected. Mutations checked: disabling the TA cache fails
  the concurrency test; disabling the backoff fails the restart test.
- `gateway/gateway_test.go`: the gateway started as `cmd/arca-gateway` does
  with `ARCA_ENV=sim`, called over TCP with a gRPC client: health, persona
  (inscripto, monotributo, not found, invalid CUIT), constatación approved
  and rejected, invalid input; real environments refuse missing
  credentials, http endpoints and unknown `ARCA_ENV`.

Not verified against real ARCA yet: homologación needs Tramo's test
certificate (WSASS). The WSCDC SOAPAction is the ASP.NET convention
(`namespace + method`) because the manual does not print it; confirm in
homologación.
