# Tablero del team

Objetivo de la semana: el núcleo en Go arranca con identidad, tenants, extractos y matching probados, y el gateway de ARCA habla con el simulador. Plan y reglas en `eng/ARQUITECTURA.md`; el esqueleto ya compila en `eng/core` (`make build`).

Límites de la máquina: Go está en `~/.local/go/bin` (el lanzador lo pone en el PATH). No hay Docker todavía: los tests usan Postgres embebido (`fergusstrange/embedded-postgres`) o se saltan con `-short`; nadie bloquea una tarea por falta de Docker.

| # | Tarea | Dueño | Estado | Nota |
|---|-------|-------|--------|------|
| 1 | Platform: chi, oapi-codegen desde `api/openapi.yaml`, pgx+sqlc, goose, middleware `Idempotency-Key`, cifrado de sobre, errores uniformes | backend | pendiente | README del módulo; `make gen` tiene que correr |
| 2 | Identity y tenants: registro, login argon2id, JWT EdDSA 10 min, refresh opaco rotado con revocación de familia, roles, RLS activa, audit log | backend | pendiente | tests: reuso de refresh revoca la familia; un tenant no ve a otro |
| 3 | arca-gateway: proto compilado con buf, WSAA (TRA, CMS, loginCms) con caché de TA, getPersona_v2, WSCDC; simulador local para `ARCA_ENV=sim` | backend | pendiente | nunca AfipSDK; la clave solo en este proceso |
| 4 | Ledger: cuentas, carga de extractos, parsers Galicia, Santander, Bancor, Macro y Mercado Pago con huella de layout, control de saldos, dedupe por hash | datos | pendiente | fixtures sintéticos por banco en `testdata/` |
| 5 | Documents: modelo de comprobante, parser del QR de ARCA (RG 4291), lectura de texto de PDF con confianza por campo | datos | pendiente | eng/arca/README.md |
| 6 | Matching: pasadas deterministas, regla guardada en cada match, bandejas auto/sugerido/excepción, aprobación con quién y cuándo | datos | pendiente | suite: los seis casos de Duotach más 200 movimientos etiquetados |
| 7 | CI de core: build, `go test -race`, vet, gosec, govulncheck, imagen a GHCR en main; Terraform mínimo (VPS, DNS, bucket) | backend | pendiente | `.github/workflows/core.yml` |
| 8 | Portal del cliente en `eng/web/app`: onboarding de tres pasos (ARCA delegada, extracto subido, ERP conectado), bandeja de aprobaciones, descarga de lo suyo | frontend | pendiente | contra el OpenAPI; mismo diseño que la landing |
| 9 | Consola del operador: clientes con pasos faltantes, tres bandejas, panel de resultados con la fuente de cada número | frontend | pendiente | |
| 10 | Especificación por módulo: flujos del portal, qué ve cada rol, checklist del DPA y del contrato con el cliente | producto | pendiente | `product/` y `ops/LEGAL.md` |
| 11 | Lista de 40 contadores de Córdoba y Río Cuarto con mensaje de apertura; one-pager para estudios | ventas | pendiente | drafts; nunca enviar |
| 12 | Postulación a HackIA fase 2 como proveedor: borrador | ventas | pendiente | uvitec.org.ar |
| 13 | QA: `make build`, `make test`, vet, gosec; que ningún secreto ni clave quede en el repo; que cada módulo tenga README y entrada en el OpenAPI; `npm run build` en web | qa | pendiente | |
