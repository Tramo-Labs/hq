# Tablero del team

Objetivo de la semana: el núcleo en Go arranca con identidad, tenants, extractos y matching probados, y el gateway de ARCA habla con el simulador. Plan y reglas en `eng/ARQUITECTURA.md`; el esqueleto ya compila en `eng/core` (`make build`).

Límites de la máquina: Go está en `~/.local/go/bin` (el lanzador lo pone en el PATH). No hay Docker todavía: los tests usan Postgres embebido (`fergusstrange/embedded-postgres`) o se saltan con `-short`; nadie bloquea una tarea por falta de Docker.

| # | Tarea | Dueño | Estado | Nota |
|---|-------|-------|--------|------|
| 1 | Platform: chi, oapi-codegen desde `api/openapi.yaml`, pgx+sqlc, goose, middleware `Idempotency-Key`, cifrado de sobre, errores uniformes | backend | hecha | db48d66 en main, qa verde (build, lint, tests -race, gen sin deriva, idempotencia 422). Pendiente para la 2: FORCE RLS y db.System acotado; test de atributos de tramo_app |
| 2 | Identity y tenants: registro, login argon2id, JWT EdDSA 10 min, refresh opaco rotado con revocación de familia, roles, RLS activa, audit log | backend | hecha | 88a36b0 + 845aa64 en main, qa verde; mutaciones de roles, allowlist y FORCE RLS en todo esquema fallan. Sin construir: link mágico, TOTP, claves de API, límite de intentos de login |
| 3 | arca-gateway: proto compilado con buf, WSAA (TRA, CMS, loginCms) con caché de TA, getPersona_v2, WSCDC; simulador local para `ARCA_ENV=sim` | backend | hecha | d5b05d8 en main, qa verde: gateway contra ARCA_ENV=sim de punta a punta, binario real con apagado limpio, clave solo en el gateway, CUITs sintéticos o citados del manual. Sin probar contra homologación (falta certificado de prueba, Martín). TA en memoria y gRPC sin mTLS: tarea 7 |
| 4 | Ledger: cuentas, carga de extractos, parsers Galicia, Santander, Bancor, Macro y Mercado Pago con huella de layout, control de saldos, dedupe por hash | datos | hecha | en main (f183bde, merge e98eaeb), qa verde: parsers, saldos, dedupe, QR y PDF con confianza, stores con FORCE RLS, aislamiento entre tenants, paths en el OpenAPI. Layouts de banco sin verificar con exportes reales |
| 5 | Documents: modelo de comprobante, parser del QR de ARCA (RG 4291), lectura de texto de PDF con confianza por campo | datos | hecha | en main (f183bde, merge e98eaeb), qa verde: parsers, saldos, dedupe, QR y PDF con confianza, stores con FORCE RLS, aislamiento entre tenants, paths en el OpenAPI. Layouts de banco sin verificar con exportes reales |
| 6 | Matching: pasadas deterministas, regla guardada en cada match, bandejas auto/sugerido/excepción, aprobación con quién y cuándo | datos | hecha | en main (edbe647), qa verde: suite sintética 200/200 (regresión, no tasa), seis casos de Duotach, decisiones por bandeja y por movimiento, reversión, aislamiento en vistas, decisiones simultáneas. El job mensual sigue como pendiente (espera River) |
| 7 | CI de core: build, `go test -race`, vet, gosec, govulncheck, imagen a GHCR en main; Terraform mínimo (VPS, DNS, bucket) | backend | hecha | en main (f581b94 + 1e66eb8), qa verde: CI completo con herramientas y acciones fijadas, Terraform, rol tramo_runtime, IP del audit probada, piso en proxies. Nunca corrió: imagen, compose de prod, workflow, gosec, govulncheck, plan/apply, ARCA real (ADR 0005) |
| 8 | Portal del cliente en `eng/web/app`: onboarding de tres pasos (ARCA delegada, extracto subido, ERP conectado), bandeja de aprobaciones, descarga de lo suyo | frontend | hecha | 77faa0a en main, qa verde (99a8662). Sin login real; alta y descarga deshabilitadas hasta tener núcleo. Vista "Registrado" espera decisión de Martín |
| 9 | Consola del operador: clientes con pasos faltantes, tres bandejas, panel de resultados con la fuente de cada número | frontend | hecha | 22a11be en main. Cuarentena, cierre semanal y Configuración (spec §6 y §4) esperan decisión de Martín |
| 10 | Especificación por módulo: flujos del portal, qué ve cada rol, checklist del DPA y del contrato con el cliente | producto | hecha | 3da4779, integrada en main: product/ESPECIFICACION.md y ops/LEGAL.md (contrato 16 puntos, DPA 12). Tres decisiones abiertas para Martín; plazos legales a verificar con abogado |
| 11 | Lista de 40 contadores de Córdoba y Río Cuarto con mensaje de apertura; one-pager para estudios | ventas | trabada | 20 de 40 con fuente, en main. No hay padrón público: falta que Martín pida el listado al Consejo o pregunte a los dos clientes. One-pager listo, sin enviar |
| 12 | Postulación a HackIA fase 2 como proveedor: borrador | ventas | borrador | en main; falta que Martín vea el formulario real y complete razón social, CUIT y referencias |
| 13 | QA: `make build`, `make test`, vet, gosec; que ningún secreto ni clave quede en el repo; que cada módulo tenga README y entrada en el OpenAPI; `npm run build` en web | qa | en curso | baseline main afd8a06: build, vet y web verdes; gosec ausente, 0 tests, platform sin README → backend |

## Pendientes técnicos sin tarea (lead, 06/11)

- backend: idempotencia sobre multipart por partes, en main.
- backend: omit_unused_structs, en main.
- datos: pasar los stores de pgx a mano a sqlc, como fija la arquitectura, en un cambio aparte.
- backend: River, vistas con security_invoker en todo esquema y test del paso ARCA con CUIT, en main (df87203, qa verde). Licencia MPL-2.0 de River: decisión de Martín.
- datos: métricas y export de matches, en main (34fc0ac, qa verde).
- frontend (sin sesión): adaptar src/lib/api a la forma real del spec (tenant del token, rutas /v1/matching, Metric con status y list), sobre fixtures.
- datos: job programado por tenant y mes, después de que backend cablee River.
- backend: límite de intentos de login en main; 429 declarado en 0454643. Dueño de base no superusuario: resuelto en tarea 7 con rol tramo_runtime.

## Reglas de convivencia (lead, 06/11)

- Sesiones activas: backend 8176a4, datos 07eb8c, qa 3a98cf, lead 214feb. Frontend, producto y ventas ya no corren; su trabajo está en main. Las tareas nuevas de frontend quedan sin sesión hasta que Martín la relance.
- `api/openapi.yaml`, `internal/platform`, el Makefile y la estructura de go.mod son de backend. Datos agrega sus endpoints y su capa de base después de que la tarea 1 esté en main.
- Migraciones por rango: backend 00002–00099, datos 00100–00199.
- Postgres embebido por puerto: backend 54329, datos 54339, qa 54349.
- `src/content/site.ts` y `product/` son de producto; frontend los lee. `src/content/app.ts` (copy del portal): frontend hace el primer borrador y después pasa a ser de producto. `growth/` es de ventas.
- Este tablero lo edita solo el lead. Una tarea se cierra con evidencia: el sha en la rama, `make build && make test` (o `npm run build`) en verde y el README del módulo.
| 14 | Pruebas como cliente: todos los escenarios por rol contra la API y la web, incluidos los de abuso; fallas en `qa/ESCENARIOS.md` | cliente | pendiente | no arregla; el lead reparte los arreglos |
| 15 | Vigilancia continua de la competencia: servicios, precios, integraciones, onboarding, tecnología visible, cambios; propuestas para Tramo | competencia | pendiente | `growth/competencia.md`; una pasada ahora y después cada ocho horas |
