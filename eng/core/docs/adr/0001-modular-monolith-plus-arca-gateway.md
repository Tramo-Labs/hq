# 0001: Modular monolith plus a separate ARCA gateway

Date: 2026-11-06. Status: accepted.

Context: two founders, no paying client yet, a product that must be sellable
as a whole. ARCA requires an X.509 certificate whose private key must not leak.

Decision: one Go binary (`core`) with modules behind clear package boundaries
and one separate process (`arca-gateway`) that alone holds the key and speaks
SOAP. gRPC between them. New services only for a security or a measured
scaling reason.

Consequences: one deploy and one schema to operate; the gateway can be
replaced when ARCA changes; a compromise of core does not expose the key.
