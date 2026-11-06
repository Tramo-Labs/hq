# 0004: ARCA gateway: own SOAP client, CMS library, ticket cache, simulator

Date: 2026-11-06. Status: accepted.

Context: ADR 0001 put the ARCA certificate in its own process. This week the
gateway needs WSAA, constancia de inscripción and WSCDC, tested without
ARCA. Rules: no AfipSDK, the key only in the gateway, secrets only by
environment.

Decisions:

1. **Own SOAP client with `encoding/xml`.** Three operations, message shapes
   copied from the manuals in `eng/arca/manuales`; responses decoded by
   local element name so namespace prefixes do not matter. The manuals'
   example responses are kept in `testdata` and decoded in tests.
2. **CMS with `github.com/smallstep/pkcs7`.** ARQUITECTURA.md says
   `crypto/pkcs7`, which does not exist in the standard library. The
   original `go.mozilla.org/pkcs7` is archived; smallstep's fork is
   maintained. **Digest SHA-256**: the WSAA spec text says SHA1+RSA, but the
   developer manual signs with `openssl cms -sign -nodetach`, whose default
   is SHA-256. To confirm in homologación; if WSAA refused it,
   `SetDigestAlgorithm` is the one line to change.
3. **Proto v1 has only the two queries**, money as integer cents (the old
   draft used `double`), no "cuit representado" (both queries run with
   Tramo's own CUIT; naming anything "delegation verified" would be false).
   CAE issuance comes in a later version of the contract.
4. **TA cache in memory, one per service, single flight, 10 minute renewal
   margin, 60 second wait after errors** (WSAA spec). Limit: a restart while
   ARCA still holds a valid TA leaves that service unavailable until it
   expires, up to 12 hours. Options for later, in order of preference:
   persist the TA encrypted (it is a 12 hour credential, less sensitive than
   the key) in a volume of the gateway; or use a separate certificate alias
   per gateway instance. Not done now: no production traffic yet.
5. **Simulator in Go, in process for `ARCA_ENV=sim`**, instead of the
   external `arca-api-sim` mentioned in `eng/arca/README.md`: no extra
   container, and it checks what matters to us (CMS signature against a
   trust pool, TRA times, `coe.alreadyAuthenticated`). Its business error
   codes are its own and say so.
6. **Gateway configuration is separate from core's.** The gateway reads only
   `ARCA_*` and `GRPC_ADDR`; core never reads `ARCA_KEY` (a test enforces
   it). In compose the gateway gets its own environment and its port is not
   published.
7. **Open: transport between core and gateway.** Plaintext gRPC without
   authentication inside the compose network today. Before production:
   mTLS with certificates issued per deployment, or at least a shared token.
   Listed for task 7 (deploy).

Consequences: the gateway can be replaced as a whole if ARCA changes; CI
never needs ARCA; the first homologación run must confirm the CMS digest and
the WSCDC SOAPAction.
