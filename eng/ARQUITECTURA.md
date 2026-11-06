# Arquitectura del núcleo (decidida el 06/11/2026)

Lo que construimos es un producto vendible entero: una empresa (o un estudio contable) lo despliega con un `docker compose up` y lo opera. Tramo es el primer operador, no el único. Todo lo de abajo sale de eso.

## 1. Forma general

Cuatro piezas, dos binarios de Go y una app web:

| Pieza | Qué hace | Por qué separada |
|-------|----------|------------------|
| `core` (Go, un binario, comandos `api` y `worker`) | API REST con OpenAPI, dominio (cuentas, extractos, movimientos, documentos, matching, reglas, aprobaciones), jobs | Monolito modular: un deploy, un esquema, módulos con límites claros. Microservicios con dos personas y cero clientes es pagar operación sin cobrar nada |
| `arca-gateway` (Go, binario propio) | Único proceso que tiene la clave privada del certificado de ARCA. Expone gRPC a `core`; habla SOAP con ARCA. Cachea el ticket de acceso por servicio y CUIT | Frontera de seguridad: si `core` se compromete, la clave no sale. Y es la pieza que se cambia entera si ARCA cambia |
| `web` (Next.js, ya existe) | Landing, portal del cliente, consola del operador | Mismo diseño; se separa en dos apps cuando el portal crezca |
| Postgres 17 | Única base. Esquemas por módulo, `tenant_id` en toda tabla de negocio, RLS activa | Una sola cosa que respaldar y auditar |

gRPC va entre `core` y `arca-gateway` (y entre cualquier servicio nuestro). ARCA es SOAP 1.2 sobre HTTPS; nadie lo expone hacia afuera.

El módulo se parte en servicio aparte solo cuando hay una razón de seguridad (como la clave de ARCA) o de escala medida. Nunca por prolijidad.

## 2. Repositorio

```
eng/core/
  cmd/api            arranque del servidor HTTP
  cmd/worker         arranque de los jobs
  cmd/arca-gateway   arranque del gateway gRPC
  api/openapi.yaml   contrato REST (spec primero; el código se genera del spec)
  proto/arca/v1      contrato gRPC del gateway
  internal/platform  config, db, logger, http, auth, idempotencia, crypto, audit
  internal/<modulo>  un directorio por feature: tenants, identity, ledger, documents, matching, rules, approvals, arca, mercadopago, erp
  migrations/        SQL versionado (goose), nunca editado después de aplicado
  deploy/            Dockerfile, docker-compose por ambiente, Caddyfile, terraform/
  docs/              una página por módulo y una ADR por decisión técnica
```

Cada `internal/<modulo>` tiene su `README.md` con: para qué existe, endpoints, tablas, jobs, eventos que emite, qué datos sensibles toca y cómo se prueban. Sin README no se mergea.

## 3. Stack de Go (fijado para no discutirlo cada semana)

| Para | Elegido | Alternativa descartada |
|------|---------|------------------------|
| Router HTTP | chi | gin: más magia, menos stdlib |
| OpenAPI | oapi-codegen, spec primero, Swagger UI en `/docs` | huma: genera el spec del código y el contrato queda escondido |
| DB | pgx + sqlc; migraciones con goose | ORM: esconde el SQL que hay que auditar |
| Jobs | River (colas sobre Postgres, jobs únicos por clave) | Temporal: otra pieza que operar; vuelve si los flujos lo piden |
| gRPC | buf + google.golang.org/grpc | connect: menos tooling conocido |
| SOAP de ARCA | cliente propio con `encoding/xml` y `crypto/pkcs7` para el CMS | AfipSDK: pasa certificados por un tercero |
| Logs | slog JSON, request id en todo | |
| Config | variables de entorno, un solo `Config` validado al arranque | archivos por ambiente: se pisan |
| Calidad | golangci-lint, gosec, govulncheck, `go test -race` en CI | |

## 4. Cómo entra un cliente (la parte de "credenciales")

No pedimos credenciales. El portal del cliente es una lista de tres pasos y cada uno se verifica solo:

1. **ARCA.** El cliente delega los web services al CUIT de Tramo desde el Administrador de Relaciones de ARCA (lo hace él, con su clave fiscal, siguiendo capturas del portal). El portal llama a constancia de inscripción con su CUIT y marca el paso como verificado cuando responde.
2. **Banco.** Dos caminos: subir el exporte del home banking (CSV, XLS o PDF) arrastrándolo al portal, o mandarlo a una casilla propia de ese cliente (`extractos+<tenant>@...`). Mercado Pago es el único con botón "Conectar" porque tiene OAuth. No hay scraping ni claves de home banking.
3. **Sistema contable.** Si el ERP tiene API (Xubio, Odoo, Alegra), el cliente genera una clave de API en su sistema y la pega en el portal. Se guarda cifrada (sección 6) y se puede revocar desde el portal.

El operador (Tramo o el estudio) ve en su consola qué pasos faltan por cliente. Lo demás del portal: bandeja de aprobaciones, panel de resultados, descarga de todo lo suyo en cualquier momento.

## 5. Sesiones, tokens y permisos

- Login con mail y contraseña (argon2id) o con link mágico por mail. Segundo factor por TOTP para operadores; obligatorio para quien aprueba pagos.
- **Access token**: JWT firmado con EdDSA, vida de 10 minutos, lleva usuario, tenant activo y rol. Nunca se persiste.
- **Refresh token**: opaco, 256 bits aleatorios, guardado como hash SHA-256 con familia y fecha. Rota en cada uso; si se reutiliza uno viejo se revoca la familia entera (robo detectado). Vive 30 días, en cookie `HttpOnly; Secure; SameSite=Strict` para el navegador y como Bearer para clientes de API.
- **Claves de API** para integraciones: con prefijo, hash en base, alcance por módulo, revocables, expiran.
- Roles por tenant: `owner`, `approver` (el cliente que aprueba), `viewer`, `operator` (Tramo), `accountant` (un estudio con acceso a varios tenants). Un usuario puede pertenecer a varios tenants.
- Cada request fija `SET LOCAL app.tenant_id` en la transacción y Postgres aplica RLS. Un bug de código no puede cruzar tenants.

## 6. Datos sensibles

- **Nunca** se almacenan claves fiscales ni de home banking. Está en el modelo de datos: no hay columna donde quepan.
- Cifrado de aplicación con sobre: una clave de datos por tenant (AES-256-GCM), envuelta por una clave maestra que vive fuera de la base (variable de entorno en dev, KMS o archivo sellado en prod). Se cifran: clave privada de ARCA (solo en el gateway), tokens OAuth, claves de API de ERPs, archivos de extractos en reposo.
- Audit log append-only: tabla con trigger que rechaza UPDATE y DELETE; cada acción registra quién, qué, sobre qué tenant, desde qué IP y con qué request id.
- Retención: documentos fiscales diez años (Ley 11.683). Baja de cliente: exporte completo y borrado programado de lo que no tenga retención legal.
- Logs sin datos personales ni montos; nada de PII en URLs.
- Residencia: Postgres en una región de nuestra elección (São Paulo o Europa), cláusulas modelo en el DPA (Ley 25.326, Estados Unidos no es país adecuado).
- Backups diarios cifrados a un bucket aparte; restauración probada una vez por mes y anotada.

## 7. Idempotencia

- Todo POST que crea algo exige `Idempotency-Key` (UUID del cliente). Se guarda `(tenant, key, hash del body, status, respuesta)` por 24 horas: misma clave y mismo body devuelve la misma respuesta; misma clave y otro body da 422.
- Los jobs son únicos por clave natural (extracto por hash del archivo, match por movimiento y documento, factura por punto de venta y número) y reintentables.
- ARCA: antes de pedir un CAE se consulta el último autorizado y nuestro propio registro de pedidos; un reintento nunca emite dos veces.
- Escrituras en ERPs: con referencia externa nuestra en cada asiento; se lee antes de escribir.

## 8. Ambientes e infraestructura

| Ambiente | Dónde | ARCA | Datos |
|----------|-------|------|-------|
| dev | docker compose local: postgres, core, gateway, simulador de ARCA, mailpit | simulador | sintéticos |
| staging | un VPS chico | homologación | anonimizados |
| prod | VPS con Caddy (TLS automático), Postgres en la misma máquina al principio, backups a bucket | producción | reales |

Imágenes construidas en GitHub Actions por commit, publicadas en GHCR con el sha. Deploy por `docker compose pull && up` en el VPS, disparado desde Actions. Terraform describe VPS, DNS y bucket desde el primer día aunque sea una sola máquina: así el mismo repo levanta un ambiente nuevo para un cliente que compre el software.

Postgres gestionado (Neon, Supabase, RDS) queda como opción documentada; se pasa cuando el costo de operar nuestro backup supere el de pagarlo.

## 9. Qué lo hace vendible entero

- Doce factores: todo por variables de entorno, nada de Tramo escrito en el código (nombre, logo y mails en configuración).
- El contrato es el OpenAPI y el proto; cualquier frontend o integrador los consume.
- Multi-tenant desde la primera migración; un cliente que compra el software es un tenant que se lleva su base, o un despliegue entero con el mismo compose.
- Migraciones versionadas, seed de ejemplo, `make up` levanta todo en una máquina limpia.
- Licencia y marca en archivos aparte.

## 10. Orden de construcción

1. Esqueleto: tres binarios que arrancan, health, migración cero, compose, CI con lint y tests.
2. Identity y tenants: registro, login, refresh con rotación, roles, RLS, audit log.
3. Ledger: cuentas, extractos subidos, parsers por banco, control de saldos, deduplicación.
4. Documents: facturas por QR de ARCA, lectura de PDF, constatación por WSCDC.
5. Matching: pasadas deterministas, reglas versionadas, bandejas, aprobaciones.
6. Gateway de ARCA contra homologación con certificado real.
7. Mercado Pago por OAuth; escritura en Xubio.
8. Portal del cliente y consola del operador sobre la API.

Cada paso cierra con su README, su parte del OpenAPI y tests que corren en CI.
