# Escenarios de cliente (tarea 14)

Primera pasada: 06/11/2026, sobre main `1078e99`. La hizo el tester cliente, que no arregla nada: el lead reparte.

**Cómo se corrió.** Corrieron api y arca-gateway (`ARCA_ENV=sim`, APP_ENV=dev, secretos efímeros) contra un Postgres 17 suelto, con los binarios de embedded-postgres, en el puerto 54359. La API quedó en :18080 y la web en :13100 (`npm run dev`). Se recorrió con Playwright headless a 1280 y 390 px. Todos los datos son sintéticos: los usuarios son `*@*-sint.test` y los extractos son los fixtures generados de `internal/ledger/testdata`. El primer operador se sembró con un INSERT en `memberships`, porque el producto no tiene forma de crearlo (ver F7). Al viewer se lo sacó del tenant también por SQL, porque no hay endpoint para eso (ver F11).

En el bloque de pasos, `POST /x {body}` lleva siempre un `Idempotency-Key` UUID nuevo, salvo que diga otra cosa. Los tokens salen de `POST /v1/auth/login` con `tokenDelivery: body`.

## Fallas

Severidad: **alta** rompe una promesa del producto o de seguridad. **Media** traba un flujo del cliente o contradice la especificación. **Baja** es un mensaje o un detalle.

| # | Capa | Escenario | Pasos exactos | Esperado | Pasó | Sev. |
|---|------|-----------|---------------|----------|------|------|
| F1 | API | Viewer reusa la clave de otro usuario del mismo tenant | 1) owner: `POST /v1/ledger/accounts {"bank":"galicia","name":"Cuenta secreta"}` con `Idempotency-Key: K`. 2) viewer del mismo tenant: el mismo POST, mismo body, misma `K`. 3) Lo mismo con una decisión: approver `POST /v1/matching/movements/{id}/decision {"action":"other","note":"honorarios"}` con `K3`, y el viewer repite con `K3`. | 403 para el viewer: su rol no puede crear cuentas ni decidir. La clave debería estar atada al usuario, o el rol debería chequearse antes del replay. | 201 al viewer en los dos casos: recibe la cuenta del owner (`id 7c8911c0…`) y la decisión del approver (`role: approver`). El mismo viewer con una clave nueva recibe 403. Entre tenants distintos la clave sí está aislada: el otro tenant crea su propia cuenta. | alta |
| F2 | API | El matching no tiene contra qué cruzar | 1) owner sube los 5 extractos OK. 2) `POST /v1/matching/runs {"period":"2026-10"}`. 3) `GET /v1/matching/matches?tray=auto\|suggested`. 4) `GET /v1/metrics?period=2026-10`. | Alguna vía en el producto para cargar las partidas abiertas (facturas, cobros esperados, ERP). Que algo caiga en Auto o en Sugerido. | `exceptions: 44, matches: 0`. Auto y Sugerido vacíos y automatización 0 % (`measured`, 0 de 44). Subir una factura no crea partidas: el único que carga partidas en `matching.items` es `UpsertItems`, y solo lo llaman los tests. Con lo que hay en la API, todo movimiento termina en Excepciones "sin candidato" y "vincular a mano" no tiene ítems que ofrecer. | alta |
| F3 | API | CSV manipulado: cambia solo la descripción | 1) owner sube `galicia_ok.csv` (aceptado, 10 movimientos). 2) Sube el mismo archivo con una sola descripción cambiada (`TRANSFERENCIA RECIBIDA CLIENTE UNO` → `=1+1 CLIENTE UNO`). 3) Otra vez, con `@SUM(A1) CLIENTE UNO`. 4) `GET /v1/exports/movements?from=2026-10-01&to=2026-10-01`. | Que se detecte como el mismo movimiento (misma cuenta, fecha, monto, saldo y comprobante `000101`) o que vaya a cuarentena. | Cada carga da `accepted, new: 1, alreadyPresent: 9`. El export muestra el mismo movimiento de $250.000 tres veces, todas con saldo 1.250.000 y comprobante 000101. El control de saldos no lo frena porque cada archivo cierra por sí mismo. Un banco que cambia el texto de un concepto entre dos exportes duplica plata. | alta |
| F4 | API | "Es otra cosa" queda trabado en el operador | 1) approver: `POST /v1/matching/movements/{id}/decision {"action":"other","note":"retiro de socio"}` → 201. 2) operator: `GET /v1/matching/pending?tray=suggested` lo muestra con `reason: client_other`. 3) operator: `POST /v1/matching/movements/{id}/decision` con cualquier acción. | Que el operador tenga una acción para resolverlo, como dice la spec §4 ("vuelve al operador"). | 403 `your role cannot do this`. La decisión por match necesita un `matchId`, y estos movimientos no tienen match. Quedan en Sugerido para siempre. | alta |
| F5 | API | Los 403 no quedan en el audit log | 1) El viewer intenta crear una cuenta, subir un extracto, correr el matching, agregar un miembro y decidir: todo da 403. 2) owner: `GET /v1/exports/audit`. | Cada intento denegado en el audit, con quién, cuándo e IP (pedido de la tarea y spec §1: "toda acción de esta tabla queda en el audit log"). | El audit tiene estas acciones: `identity.login, identity.member_added, identity.refresh_reuse, identity.register, ledger.account_created, ledger.file_quarantined, ledger.statement_accepted, matching.movement_other, matching.movement_revert, matching.run`. No hay ni una denegación. | alta |
| F6 | API | Viewer y approver descargan todo, incluido el audit | viewer y approver: `GET /v1/exports/audit`, `/movements` y `/matches`. | 403. La spec §1 dice "Descargar todo" y "Ver el audit log" solo para owner, accountant y operator. | 200 para los dos. El viewer se lleva 44 movimientos y 38 filas de audit, con user_id e IP de cada acción. | alta |
| F7 | API | Nadie puede crear al primer operador, ni el dueño a su contador | 1) owner: `POST /v1/tenant/members {"email":…,"role":"operator"}`. 2) Lo mismo con `"role":"accountant"`. | Spec §1: el owner invita usuarios del tenant, y el contador del cliente es `accountant`. Algún camino de alta del operador de Tramo. | 403 en los dos. Solo un operator puede dar operator o accountant, y no hay forma de crear al primero desde el producto. Para seguir probando hubo que sembrarlo por SQL. | media |
| F8 | API | Invitar a alguien que todavía no tiene cuenta | 1) owner: `POST /v1/tenant/members {"email":"aprob-nuevo@…","role":"approver"}` → 201. 2) Esa persona intenta entrar. | Que tenga una vía para entrar. | El usuario se crea sin contraseña, y el link mágico no está construido (lo dice la spec). No hay registro ni reset que le sirva: el login da 401 y `register` da `email_taken`. Solo funciona si la persona se registra antes, y eso le crea un tenant propio que nadie usa. | media |
| F9 | API | Mismo archivo, otra cuenta | 1) owner sube `galicia_ok.csv` a la cuenta Galicia (aceptado). 2) Sube el mismo archivo a la cuenta Santander. | `quarantined, bank_mismatch` ("file is a galicia export, account is santander"), como pasa en un tenant limpio. | 200 `duplicate`, "this exact file was already uploaded". El cliente entiende que Santander quedó cargada. | media |
| F10 | API | Bloqueo de la cuenta del dueño por intentos ajenos | 1) Cuatro logins con contraseña mala para un email (sumados a uno previo). 2) Login con la contraseña correcta, desde la misma IP. | Frenar al atacante sin dejar afuera al dueño, o darle una salida al dueño. | 429 al quinto fallo, `Retry-After: 900`. Con la contraseña correcta también da 429 durante 15 minutos. En el código hay además un tope de 20 fallos por hora por email desde cualquier IP; eso lo leí, no lo probé. | media |
| F11 | API | Sacar a una persona del tenant | Buscar en el spec un endpoint para quitar un miembro. | Spec §1 ("invitar y quitar") y §7 (se le corta el acceso en menos de 10 minutos). | No existe el endpoint. Sacándolo por SQL el corte funciona: el refresh da 401 y el access token sigue vivo hasta que vence (10 minutos). | media |
| F12 | API | Ver los movimientos y los archivos subidos | Buscar en el spec una lista de movimientos y de extractos subidos. | Spec §4, "Archivos": lista de extractos y documentos con fecha, quién lo subió y estado. El cliente "ve los movimientos". | Los movimientos solo salen en CSV (`/v1/exports/movements`). Los extractos aceptados no se listan en ningún lado: solo hay `/v1/ledger/quarantine`. | media |
| F13 | API | El operador da roles que la spec no le da | operator: `POST /v1/tenant/members` con `role: accountant` y con `role: operator`. | Spec §1: el operator invita "solo al primer owner". | 201 en los dos casos. | media |
| F14 | Web (datos de ejemplo) | Las acciones del portal no coinciden con las de la API | Abrir `/app/aprobaciones`. | Los botones de cada ítem deberían mapear a las acciones de la API. | "Sin candidato" ofrece **Rechazar**, pero la API, para un movimiento sin candidato, solo acepta `other`, `link` y `revert`: no tiene reject. "Vincular a mano" (`link`), que sí existe en la API, no aparece. Los motivos "posible duplicado" y "sobre el umbral" del portal y del panel (`?motivo=over_threshold`) la API nunca los produce. Al conectar la web, esto no va a cerrar. | media |
| F15 | API | "Es otra cosa" sin nota | owner: `POST /v1/matching/movements/{id}/decision {"action":"other"}`. | 400 con "note required". | 400 "this action is not available in the item's tray": el cliente cree que eligió mal la acción. | baja |
| F16 | API | CSV con una comilla rota | Subir `galicia_ok.csv` con una descripción `=HYPERLINK("http://x","a")`. | `row_parse` con la línea donde falla. | `unknown_layout`, "header fingerprint  is not a known layout", con la huella vacía. El motivo no ayuda a nadie a corregir el archivo. | baja |
| F17 | API | Mensajes de 400 | Cualquier body o parámetro fuera de schema (por ejemplo `period=2026-13`). | Un detalle corto y estable. | El `detail` trae el schema JSON entero con saltos de línea. Al cliente no le sirve y muestra cosas internas. | baja |
| F18 | API | Entrar a un tenant ajeno con login | rival: `POST /v1/auth/login` con su contraseña correcta y `tenantId` de Molino. | 401 o 403 con un motivo del tipo "no sos miembro". | 401 "invalid email or password", con la contraseña correcta. Para el contador que se equivoca de cliente, el mensaje confunde. | baja |
| F19 | Web (datos de ejemplo) | Rechazar sin motivo | `/app/aprobaciones` → Rechazar. | Spec §4: rechazar "vuelve al operador con el motivo". | El campo dice "¿Por qué lo rechazás? opcional". | baja |

No se reportan, porque ya se conocen: que la web no tenga login, que la web no llame al núcleo (cero requests a `/v1/` en todas las rutas) y que las decisiones del portal y de la consola se pierdan al recargar. Las tres son porque la web corre con datos de ejemplo, y la web lo avisa en pantalla.

## Lo que pasó bien

| Escenario | Evidencia |
|-----------|-----------|
| Registro, empresa y onboarding | `register` 201. `/v1/me/tenant`: el paso de banco pasa a `verified` con el primer extracto aceptado, ARCA queda `pending` sin CUIT y ERP queda `pending`. |
| Un extracto de cada banco | galicia/csv-v1, santander/csv-v1, bancor/csv-v1 y macro/txt-v1 con `new: 10` cada uno. mercadopago/release-v1 con `new: 4`. Todos `accepted`. |
| Repetir una carga no duplica | El mismo archivo da 200 `duplicate, new 0`. Dos partes que se solapan dan `alreadyPresent 7` y `6`, con `new 0`. El mismo archivo con CRLF da `alreadyPresent 10`, `new 0`. La misma clave con el mismo archivo y otro nombre da 422. |
| Cuarentena con motivo | Salen `balance_mismatch` (con la línea y la diferencia), `incomplete`, `row_parse` (línea y columna), `unknown_layout`, `chain_gap` (diferencia 5000.00), `bank_mismatch`, `account_mismatch` y `empty`. `GET /v1/ledger/quarantine` los lista con el motivo. |
| Archivos malformados o enormes | Vacío: 400. 4 KB de bytes al azar y un binario MZ: cuarentena `unknown_layout`. UTF-16: cuarentena. Una línea de 5 MB: `row_parse`. 30 MB: 413 `payload_too_large`. Un body que no es multipart o con el campo mal: 400. Una factura que no es PDF: 400. |
| Viewer | 403 al crear cuenta, subir extracto, subir factura, correr el matching, agregar miembro, decidir y revertir, y en `/v1/tenants`. Puede leer pendientes. |
| Approver | Decide Excepciones (`other` 201, revision 1, role approver). Un segundo intento da 409 `conflict`. El owner revierte (revision 2) y otro revert da 409 `nothing_to_revert`. 403 al crear cuenta, correr el matching y agregar miembro. Subir extractos le está permitido, como dice la spec. |
| Contador con tres clientes | Login con `tenantId` en cada cliente. Los CSV de movimientos de los tres no comparten ni una fila (0, 0, 0). Las cuentas de cada uno son solo las suyas. El refresh mantiene el tenant. Con el token de Otra Pyme, subir a una cuenta de Tercera da 404. 403 al decidir, correr el matching y agregar miembros. Puede subir extractos. `/v1/tenants` muestra sus tres. |
| Operador | Ve sus tenants en `/v1/tenants`. Da de alta cuentas y extractos y corre el matching en dos clientes. Decidir Excepciones del cliente da 403. |
| Refresh viejo | Reusar r1 da 401 `refresh_token_reused`, y r2, el de la misma familia, queda muerto (401). Queda en el audit como `identity.refresh_reuse`. |
| Idempotency-Key | Misma clave y mismo body: replay del mismo `id`. Misma clave con otro body: 422 `idempotency_key_reused`. Sin clave: 400. Una clave que no es UUID: 400. Entre tenants, la clave está aislada. |
| Recursos de otro tenant por id | Cuenta, movimiento y factura ajenos dan 404. `pending` del rival vacío. Export del rival: 0 movimientos. |
| Tokens | Sin token, token basura y `alg: none`: los tres 401. |
| Fuerza bruta | 429 al quinto fallo, con `Retry-After: 900`. Otro email desde la misma IP entra igual. El registro se frena a los 10 por hora por IP. No hay enumeración: email desconocido y contraseña mala dan el mismo 401. |
| Validaciones del registro | Email repetido: 409 `email_taken`. Slug repetido: 409 `slug_taken`. CUIT con dígito verificador malo: 400 `invalid_cuit`. Contraseña corta: 400. El email no distingue mayúsculas. |
| Fórmulas en el export | `=1+1…` y `@SUM(A1)…` salen como `'=1+1…` y `'@SUM(A1)…`. |
| Métricas | `no_data` con value null cuando no hay decisiones, distinto de 0. Cada métrica trae `source` y `list`. |
| Web en 1280 y 390 | Las 8 rutas (`/`, `/app`, `/app/aprobaciones`, `/app/resultados`, `/app/descargas`, `/operador`, `/operador/bandejas`, `/operador/resultados`) dan 200 y no tiran errores de consola. A 390 px ninguna tiene scroll horizontal (scrollWidth = 390): la marquesina de la landing desborda pero queda recortada. |
| Swagger | `GET /docs/` da 200. |

## Tanda 2: `make dev` de c256cf0 (06/11/2026)

**Cómo se corrió.** Se tomó la rama worktree-backend en c256cf0 (a2dc814, que es lo que verificó qa, más F1) con `git archive` a un directorio temporal. Se corrió `make dev` (el binario `cmd/dev`) con `DEV_PG_PORT=54369`, `DEV_HTTP_ADDR=127.0.0.1:18080`, `DEV_GRPC_ADDR=127.0.0.1:19090` y `DEV_DATA_DIR` en /tmp. Se usaron los usuarios sembrados: owner, approver y viewer @alfa.test, accountant@estudio.test, operator@tramo.test y owner@beta.test. No hubo siembra manual.

### Estado de la tanda 1

| # | Estado en c256cf0 | Evidencia |
|---|-------------------|-----------|
| F1 | **resuelta** | Con la clave del owner y el mismo body, viewer y approver reciben 403. El replay del propio owner devuelve el mismo `id`, y con otro body da 422. Sobre una decisión, la clave del approver usada por el viewer o por el owner da 422 `key used for another decision`: no hay replay ajeno. |
| F2 | sigue | La semilla carga partidas por adentro (`matching.items_loaded` en el audit) y deja 1 match en Auto y 1 en Sugerido. Pero el spec sigue sin un path para cargarlas (lista de paths igual a la de la tanda 1). Para un cliente real, todo sigue cayendo en Excepciones. |
| F3 | sigue | El Galicia con `CLIENTE UNO` → `CLIENTE 1` da `new: 1, alreadyPresent: 9`. El export muestra dos filas de $250.000 con saldo 1.250.000 y comprobante 000101. Además infla las métricas: el denominador de automatización pasó de 10 a 21 movimientos. |
| F4 | sigue | El client_other sembrado aparece en el Sugerido del operador. Si el operador lo decide, `revert` y `other` dan 403 y `link` da 400. |
| F5 | sigue | Después de dos 403 del viewer, el audit tiene solo `identity.login, ledger.account_created, ledger.statement_accepted, matching.items_loaded, matching.movement_other, matching.run`. |
| F6 | sigue | viewer y approver: movements, matches y audit dan 200, 200 y 200. |
| F7 | sigue | El owner da accountant u operator: 403. |
| F8 | sigue | Al invitado nuevo, el login le da 401. |
| F9 | sigue | El mismo archivo en la cuenta Santander da 200 `duplicate`. |
| F10 | sin repetir | No quise bloquear a los usuarios sembrados en medio de la pasada. |
| F11, F12 | siguen | No hay paths nuevos en el spec. |
| F13 | sigue, y más amplio | El operator da accountant, operator, **owner** y approver: 201 en los cuatro. |
| F14, F19 | sin cambio | La web no cambió en esta rama. |
| F15, F16, F17, F18 | siguen | Mismos mensajes que en la tanda 1. |

### Fallas nuevas

| # | Capa | Escenario | Pasos exactos | Esperado | Pasó | Sev. |
|---|------|-----------|---------------|----------|------|------|
| F20 | API | El cliente ve la bandeja Sugerido | owner y viewer de alfa: `GET /v1/matching/matches?tray=suggested` y `GET /v1/matching/pending?tray=suggested`. | Spec §1: "el cliente nunca ve la bandeja Sugerido ni reglas internas sin confirmar". 403 o lista vacía para owner, approver y viewer. | 200 con el match sugerido (regla `amount_tolerance` y el ajuste de redondeo). El pending trae 3 movimientos: los `ambiguous`, con todas las candidatas del operador, y el `client_other`. | alta |

### Lo que pasó bien en la tanda 2

| Escenario | Evidencia |
|-----------|-----------|
| Nadie decide Auto | owner y operator, `approve` sobre el match de Auto: 403. |
| El cliente no decide Sugerido | owner, approver, viewer y accountant, `approve` sobre el sugerido: 403. |
| Operador: Sugerido → Excepciones | `escalate` 201 (revision 1, role operator). El doble toque con la misma clave devuelve la misma decisión. Una segunda decisión del operador da 403. El match aparece en Excepciones. |
| El operador no aprueba excepciones ajenas | operator `approve` en Excepciones: 403. viewer y accountant: 403. `escalate` en Excepciones: 400. |
| El approver decide Excepciones | `approve` 201 (revision 1, role approver, tray exception). El doble toque con la misma clave da la misma respuesta y no aplica dos veces. Un segundo approve del owner da 409. El ítem sale de la bandeja y `openExceptions` baja de 6 a 5. |
| Revertir | El owner revierte: revision 2. El viewer revierte: 403. En el audit quedan `matching.escalate`, `matching.approve` y `matching.revert`, cada uno con user_id e IP. |
| Export de matches | Cada fila trae la regla, la versión, el ajuste `rounding:0.75` y la decisión vigente (quién, rol, cuándo, revisión). |
| Contador con dos clientes sembrados | alfa tiene 21 filas, beta 10, y comparten 0. `/v1/tenants` muestra solo alfa y beta. |
| Otro tenant por id | El owner de alfa sube a una cuenta de beta: 404. Login del owner de beta con el `tenantId` de alfa: 401. |

## Tanda 3: facturas con QR y login por cookie (mismo `make dev` de c256cf0)

Las facturas son PDFs mínimos generados en el momento. Cada uno lleva un link annotation con un QR RG 4291 que usa el emisor de ejemplo del manual (30000000007) y, como receptor, el CUIT sintético de alfa (30000000015).

### Fallas nuevas

| # | Capa | Escenario | Pasos exactos | Esperado | Pasó | Sev. |
|---|------|-----------|---------------|----------|------|------|
| F21 | API | Factura dirigida a otra empresa | owner de alfa: `POST /v1/documents/invoices` con un PDF cuyo QR trae `nroDocRec: 30000000023` (el CUIT de beta). | Que la factura quede retenida para revisar, o que se rechace: no es del cliente. | 201 `autoEligible: true`, con todos los campos `trusted` y confianza 0.98. Nadie compara el receptor del QR con el CUIT del tenant (el README de documents solo excluye el CUIT propio como emisor). Una factura ajena entra como apta para registrar en automático. | media |
| F22 | API | Refresh por cookie desde otro origen | 1) login sin `tokenDelivery` → cookie `tramo_refresh`. 2) `POST /v1/auth/refresh` con esa cookie y `Origin: https://evil.example`. | Rechazo por origen como segunda defensa, aunque SameSite=Strict ya frena al navegador. | 200 con un access token nuevo. Hoy lo protege solo SameSite. | baja |

Con F2 hay evidencia nueva. Subí una factura con QR válido por $8.500 con fecha 2026-10-02, igual que un débito de alfa del mismo día, y corrí el matching del período. El débito sigue en Excepciones y Auto sigue con 1. La factura no alimenta al matching ni siquiera cuando el monto y la fecha coinciden exacto.

### Lo que pasó bien en la tanda 3

| Escenario | Evidencia |
|-----------|-----------|
| Factura con QR | 201 `autoEligible: true`. Cada campo trae `source: arca_qr`, confianza 0.98 y `trusted`. `GET /v1/documents/invoices/{id}` devuelve lo mismo. |
| Factura repetida | El mismo archivo da 409 "this file was already uploaded". La misma factura en otro PDF da 409 "this invoice was already uploaded in another file". |
| QR inválido | Importe con 3 decimales o `tipoCodAut: Z`: 201, pero sin campos, `autoEligible: false` y el motivo en `trail`. Las dos aparecen en `?autoEligible=false`. |
| Contador sube factura | 201, como dice la spec. |
| Cookie del navegador | `tramo_refresh` con `Path=/v1/auth; HttpOnly; Secure; SameSite=Strict` y 30 días. El refresh no viaja en el body. Rota con cada refresh. El logout la borra (`Max-Age=0`), y después el refresh da 401. |
| CORS | Un preflight desde otro origen no recibe `Access-Control-Allow-Origin` (405). |

## Sin probar todavía (hasta la tanda 3)

El cierre de período (`period_closed`): todavía no existe. No hay endpoint para cerrar un período y cómo se cierra depende de una decisión de Martín, así que se prueba cuando esté construido. La factura con QR dentro de una imagen o en la capa de texto (probé solo el link). El gateway ARCA desde la API, que el núcleo no usa en ningún endpoint del cliente. El tope de 20 fallos por hora por email desde varias IPs: el lead pidió no repetir F10.

## Tanda 4: `make dev` de main 10d396f (06/11/2026)

La hizo cliente-2. Corrió `make dev` con datos nuevos (`DEV_DATA_DIR` propio, API en :18080, gateway en :19090, Postgres en :54361, `DEV_APP_BASE_URL=http://localhost:13100`). La web corrió con `CORE_API_URL=http://127.0.0.1:18080 next dev -p 13100` y se recorrió con Playwright headless a 1280 y 390 px. Todo es sintético: los usuarios sembrados `*.test`, los nuevos `*-sint.test` y los fixtures de `internal/ledger/testdata`. Para F10 usé otras direcciones de loopback como origen (`curl --interface 127.0.0.x`). Las pruebas dejaron al approver sembrado con 429 durante 15 minutos desde 127.0.0.1.

### Estado de las tandas 1 a 3

| # | Estado | Evidencia |
|---|--------|-----------|
| F1 | resuelta | Ya estaba resuelta en la tanda 2. Se repitió: viewer y approver con la clave del owner reciben 403. El replay del owner devuelve el mismo `id` y con otro body da 422 `idempotency_key_reused`. |
| F2 | resuelta en lo que se pidió | Subí una factura con QR por el mismo importe y la misma fecha del `PAGO PROVEEDOR EJEMPLO SA` (−120.500,50, 02/10). `GET /v1/matching/items` pasa de 2 a 3 partidas (`source: document`). Después de `POST /v1/matching/runs` el pago deja Excepciones y pasa a Sugerido con `shared_candidate`. Es correcto: el mismo pago está en los fixtures de cuatro bancos. Las partidas del ERP siguen sin un camino de carga. |
| F3 | resuelta | El Galicia con `CLIENTE UNO` → `CLIENTE 1` queda `quarantined, movement_conflict`, "line 11 has the date, amount, balance and references of a movement already stored, with another description". `new: 0`. |
| F4 | resuelta | approver `other` con nota: 201. El movimiento aparece en Sugerido del operador. operator `resolve`: 201 (revision 2). operator `other`: 403. |
| F5 | resuelta | Cada 403 deja una fila `authz.denied` con `operation:…`, user_id, request_id e IP. Los repetidos se agrupan en una fila con `repeated` y `repeated_until`. |
| F6 | resuelta | viewer y approver: audit, movements y matches dan 403 "your role cannot download the tenant's data". |
| F7 | resuelta | El owner da accountant, owner, approver y viewer: 201. Si da operator, 403: el primero sale del comando `api create-operator`, como dice el spec. |
| F8 | resuelta en la API, no en la web | Al invitado sin contraseña le llega un mail al outbox. `POST /v1/auth/invite/accept` da 200 y después el login con la contraseña nueva funciona. Pero el link del mail da 404 en la web: ver F23. |
| F9 | resuelta | El Galicia subido a la cuenta Santander queda `quarantined, bank_mismatch`, "file is a galicia export, account is santander". |
| F10 | resuelta | Seis fallos desde 127.0.0.2: el sexto da 429 con `magicLink: true`. El titular entra desde 127.0.0.3 con 200. Veinte fallos desde seis IPs: el login pasa a `magic_link_required`, también con la contraseña correcta. Un email que no existe recibe la misma respuesta, así que no sirve para averiguar qué emails hay. |
| F11 | resuelta | `DELETE /v1/tenant/members/{id}`: 204. Después de eso, al sacado el login y el cambio de empresa le dan 403 `not_a_member`. Si el owner intenta sacar al último owner, 409 `last_owner`. |
| F12 | resuelta | `GET /v1/ledger/files` lista cada archivo con quién lo subió, la fecha, el estado y el motivo. `GET /v1/ledger/movements?period=2026-10` da 200. |
| F13 | resuelta para un tenant con owner | operator da accountant, operator, owner o approver: 403 en los cuatro. El caso "solo mientras no hay owner" no se pudo armar sin el comando. |
| F14 | sin repetir del todo | En vivo, Excepciones ofrece "Es otra cosa" y "Vincular a mano", que corresponden a `other` y `link`. Cuando recorrí la web no había un match con candidato en Excepciones, así que "Rechazar" no apareció. |
| F15 | resuelta | `other` sin nota da 400 `note_required`, "note required: say why". |
| F16 | resuelta | Con una comilla suelta: `row_parse`, "line 2: bare \" in non-quoted-field". Con una comilla sin cerrar: `row_parse`, "line 11: extraneous or missing \" in quoted-field". |
| F17 | resuelta | `period=2026-13` da "invalid parameter \"period\"". Un body sin `name` da "invalid request body: /name". |
| F18 | resuelta | El owner de beta con el `tenantId` de alfa: 403 `not_a_member`. |
| F19 | resuelta, con una variante nueva | `reject` sin nota o con espacios: 400 `note_required`. Una nota hecha solo de un carácter invisible pasa: ver F28. |
| F20 | resuelta | owner, viewer y approver piden Sugerido (matches y pending): 403. |
| F21 | resuelta | Factura con `nroDocRec` de beta: 201 `autoEligible: false`, `itemReason: not_this_tenant`. |
| F22 | resuelta | Refresh por cookie con Origin `https://evil.example`, sin Origin, `null` o `http://localhost:13100.evil.example`: 403 `origin_not_allowed`. Con el origen de la web: 200. |

### Fallas nuevas

| # | Capa | Escenario | Pasos exactos | Esperado | Pasó | Sev. |
|---|------|-----------|---------------|----------|------|------|
| F23 | Web + API | Los links de los mails llevan a páginas que no existen | 1) owner: `POST /v1/tenant/members {"email":"nuevo-viewer@alfa-sint.test","role":"viewer"}`. 2) Abrir el link del outbox. 3) `POST /v1/auth/magic-link {"email":"approver@alfa.test"}` y abrir ese link. | Una página que acepta la invitación (contraseña y nombre) y otra que abre la sesión con el link. | Los links son `http://localhost:13100/invitacion#token=…` y `http://localhost:13100/ingresar#token=…`. Los dos dan 404 en la web: `curl` da 404 en los dos, y en la web no hay código de invitación, link mágico ni cambio de empresa por refresh. El invitado no tiene forma de entrar desde el producto. | alta |
| F24 | Web | Con el login frenado, la web no ofrece el link mágico | 1) Cinco logins malos desde la misma IP. 2) Entrar desde `/entrar` con la contraseña correcta. | Spec: el 429 trae `magicLink: true` porque "the person can still get in with POST /v1/auth/magic-link". La web debería ofrecer el link. Con `magic_link_required` (D-17), el link es la única forma de entrar. | La web muestra "Hubo demasiados intentos. Probá de nuevo en 13 minutos." y no ofrece el link. Con un ataque repartido entre varias IPs, la persona queda afuera una hora aunque sepa la contraseña. | media |
| F25 | API | El owner de otra empresa cambia el email guardado de un usuario ajeno | 1) owner de beta: `POST /v1/tenant/members {"email":"APPROVER@ALFA.TEST","role":"viewer"}`. 2) owner de alfa: `GET /v1/tenant/members`. 3) approver: `GET /v1/me`. | Que alta en beta no toque nada de la cuenta en alfa. El email guardado no lo cambia nadie más que su dueño. | 201. Desde ese momento alfa lo ve como `APPROVER@ALFA.TEST`, y el `/v1/me` del approver devuelve `APPROVER@ALFA.TEST`. Un alta que el owner de alfa vuelve a intentar con otras mayúsculas y que da 409 `already_member` también le cambia el email guardado. Por ahora solo cambia las mayúsculas, pero es una escritura de un tenant sobre datos que ve otro. | media |
| F26 | API | Sumar a cualquiera con cuenta, sin aviso | 1) owner de beta: `POST /v1/tenant/members {"email":"viewer@alfa.test","role":"viewer"}`. 2) Mirar el outbox. 3) viewer: `GET /v1/me`. | Que la persona se entere: un mail, o una invitación que tenga que aceptar. | 201, y el outbox no tiene ningún mail para `viewer@alfa.test`. Beta aparece en `memberships` y en el selector de empresa del viewer. Cualquiera que se registre puede meter su empresa en la cuenta de otra persona con el nombre que quiera (ver F27). La respuesta no expone el nombre de la persona: `name: null` aunque ella lo haya cargado. | media |
| F27 | API + Web | Nombres de empresa y de cuenta con caracteres invisibles o de inversión de texto | 1) `POST /v1/auth/register` con `tenantName` `"​​​"`, `"ㅤㅤ"`, `"  "` y `"Tramo Soporte ‮)ved( ofla oniloM"`. 2) owner: `POST /v1/ledger/accounts` con `name` `"​​"`, `"ㅤ"` y `"Cuenta ‮laicnes"`. 3) Ese owner invita a alguien. | Las mismas reglas que se pusieron para el nombre de la persona en 10d396f: algo visible, sin controles bidi. | 201 en los siete casos. En el alta del portal las cuentas se ven en blanco y con el texto invertido. El mail de invitación dice "You were added to Tramo Soporte ‮)ved( ofla oniloM as viewer.", y el lector de correo invierte el resto de la línea. También se puede registrar una empresa que se llame "Tramo Soporte". El nombre de la persona con los mismos caracteres da 400 `invalid_name`. | media |
| F28 | API | Motivo de rechazo invisible | operator escala el sugerido. owner: `POST /v1/matching/matches/{id}/decision {"action":"reject","note":"​"}`. | 400 `note_required`, igual que con espacios. | 201 con `note: "​"`. El operador recibe un rechazo "con motivo" que en realidad no tiene texto. | baja |
| F29 | API | Volver a sumar a alguien que no llegó a entrar | 1) owner invita a `nuevo-approver@alfa-sint.test`. 2) Lo saca (`DELETE`, 204). 3) Esa persona abre la invitación: 403 `not_a_member`. 4) owner lo suma de nuevo: 201. 5) Login con la contraseña que puso en el paso 3. | El paso 3 no cambia nada. Si lo vuelven a sumar, le llega una invitación nueva. | El paso 3 da 403 pero guarda la contraseña igual: en el paso 5 entra con 200. El paso 4 no manda ningún mail (hay uno solo para esa dirección) y el token viejo da 401. Si la persona no hubiera usado el link antes, ahora no tendría cómo entrar desde la web (F23, F24). | media |
| F30 | API | Email de miembro con un carácter invisible | owner: `POST /v1/tenant/members {"email":"owner​@alfa.test","role":"viewer"}`. | 400: no es un email válido. | 201: crea un usuario nuevo y le manda una invitación. En la lista de miembros se ve idéntico a `owner@alfa.test`. | baja |
| F31 | API | Mails al cliente en inglés | Ver el outbox después de una invitación y de un link mágico. | Castellano, como el resto del producto (mercado AR). | "Subject: Your invitation", "You were added to Molino Alfa (dev) as viewer.", "Your sign in link". El rol también sale en inglés. | baja |

### Lo que pasó bien en la tanda 4

| Escenario | Evidencia |
|-----------|-----------|
| Una carga por banco | Galicia, Santander, Macro y Mercado Pago: 201 `accepted` con 10, 10, 10 y 4 movimientos y su `layoutId`. El mismo Galicia otra vez: 200 `duplicate`, `new: 0`. |
| Invitación | Token corto: 400. Token desconocido: 401 `invalid_invite`. Contraseña débil: 400. Aceptar: 200. Reusar el token: 401. Nombre con un carácter invisible: 400 `invalid_name`. El mismo email con otras mayúsculas da 409 `already_member`, sin duplicar la persona. |
| Link mágico | Un email que no existe y uno que existe: 202 en los dos. El link se usa una vez y el segundo uso da 401 `invalid_magic_link`. Pedido para un tenant ajeno: el accept da 403 `not_a_member`. A un invitado sin contraseña le sirve para entrar. Con el mismo `Idempotency-Key` y otro email: 422. Doce pedidos seguidos: 2 dan 202 y 10 dan 429. |
| Cambio de empresa (API) | Contador de alfa a beta: 200, y `/v1/me` muestra beta activa con rol accountant. Los archivos de beta no traen ninguna cuenta de alfa. Reusar el refresh viejo: 401 `refresh_token_reused`. Un tenant ajeno o inexistente da 403 `not_a_member` y el refresh sigue sirviendo. Con el access token en lugar del refresh: 401. Por cookie con un origen ajeno: 403 `origin_not_allowed`. Después de que lo sacan: 403. |
| Cambio de empresa (web, 390 px) | El contador entra, elige Alfa y va a "Cambiar de empresa". Confirma la contraseña y elige Beta: el encabezado pasa a "Molino Beta (dev)" y la página no menciona Alfa. |
| Nombre de persona | `PATCH /v1/me` rechaza con `invalid_name` 16 variantes: solo ZWSP, solo NBSP, el relleno Hangul, el blanco Braille, RLO, ZWJ, solo combinantes, tab, salto de línea, BOM, caracteres tag, soft hyphen, word joiner, solo selector de variación y el separador mongol. Más de 120 caracteres: 400. Acentos y emoji: 200. En el registro, un nombre con RLO da 400. |
| CORS | Al preflight desde `http://localhost:13100` vuelve ese origen exacto, con credenciales y los tres headers. Desde las mayúsculas, la barra final, https, otro puerto, 127.0.0.1, un subdominio parecido, `evil.localhost` o `null`: 403 sin `Access-Control-Allow-Origin`. Un GET con origen ajeno no recibe ACAO, así que el navegador no puede leerlo. |
| Portal a 390 px | `/app`, `/app/aprobaciones`, `/app/resultados` y `/app/descargas`, y lo mismo a 1280: sin scroll horizontal y sin errores de consola ni respuestas 4xx. El owner que entra en `/operador` ve "La consola es para operadores y contadores" con un link al portal. |
| Decidir desde el celular | owner, "Es otra cosa": la nota es obligatoria en el formulario y el POST da 201 con `{"action":"other","note":…}`. En "Vincular a mano", con "Todos los meses" aparecen las dos facturas de agosto. Al elegirlas, el total dice "Coincide" y el POST da 201 `link` con los dos `itemIds`. El contador baja de 5 a 4. |
| Las decisiones sobreviven a una nueva corrida | Después de `POST /v1/matching/runs` para 2026-10, el `other` y el `link` de Bancor no vuelven a Excepciones. El resuelto por el operador tampoco. |

### Sin probar en la tanda 4

Los vencimientos: la invitación a las 72 horas y el link mágico a los 15 minutos. Que el access token de alguien a quien sacaron deje de servir en 10 minutos. El grant del operator a un tenant sin owner, porque hace falta el comando `create-operator`. El botón "Rechazar" del portal en vivo. El cierre de período sigue sin construir.

### Rechequeo de F23 y F24 sobre main 7bfc442 (06/11/2026)

Volví a levantar `make dev` desde 7bfc442 con el mismo directorio de datos. La web es un build de producción: `PORTAL_ENABLED=1 NEXT_PUBLIC_API_BASE=http://localhost:18080 next build`, servido con `next start -p 13100`. El navegador llama directo a la API con CORS. El build compila `/ingresar` e `/invitacion`. Todo se recorrió con Playwright a 390 px y con usuarios nuevos `*-sint.test`.

| # | Estado | Evidencia |
|---|--------|-----------|
| F23 | resuelta | **Invitación**: el link del outbox abre "Sumate a Tramo." y la persona completa nombre y contraseña dos veces. Hay `200 POST /v1/auth/invite/accept` y entra a `/app` con "Inés Invitada · aprobador". Al recargar, la sesión sigue por la cookie (`200 POST /v1/auth/refresh`). Si se reusa el link, aparece "La invitación ya se usó o venció…". `/invitacion` sin token muestra "Este enlace está incompleto…". **Link mágico**: el link abre `/ingresar`, con `200 POST /v1/auth/magic-link/accept`, y la persona entra a `/app`. Si se reusa, aparece "El enlace ya se usó o venció…". **Cambio de empresa sin contraseña**: un usuario que entró por link y es viewer de alfa y beta va a "Cambiar de empresa". La pantalla no pide contraseña: elige Beta y hay `200 POST /v1/auth/switch-tenant`. El encabezado pasa a "Molino Beta (dev)", y después de recargar no aparece Alfa. |
| F24 | resuelta | **429 por email e IP**: tras 6 contraseñas malas desde la web, la contraseña buena muestra "Hubo demasiados intentos. Probá de nuevo en 15 minutos." y además "Podés entrar con un enlace…" con el botón "Mandame un enlace". El botón hace `202`, el link entra a `/app` como "Vicente Visor · lectura". **`magic_link_required`**: hubo 20 fallos desde 7 IPs de loopback, y después la contraseña buena da `401` y la web muestra "Por seguridad, a esta cuenta hoy se entra con un enlace por mail." y el botón. El botón hace `202 POST /v1/auth/magic-link`, y el link hace `200 …/accept` y entra a `/app`. |

Una nota sobre F24. Después de reiniciar el núcleo, la cuenta que estaba en modo "solo con link" volvió a entrar con contraseña (200). No es una falla nueva: los contadores viven en memoria de cada proceso de la API, y así lo dice el README de identity ("counters are in memory, per api process"). Para la prueba volví a disparar el ataque repartido.

#### Falla nueva

| # | Capa | Escenario | Pasos exactos | Esperado | Pasó | Sev. |
|---|------|-----------|---------------|----------|------|------|
| F32 | Web | Sin contraseña y sin la invitación, no hay forma de pedir el link | 1) owner: `POST /v1/tenant/members` con un email nuevo (`inv-…@alfa-sint.test`); la persona no abre la invitación (se pierde o vence a las 72 h). 2) En `/entrar` pone su email y cualquier contraseña. | Que la web le ofrezca "Mandame un enlace". La API se lo da: el link mágico sirve para quien no tiene contraseña (tanda 4). | `401` y "El email o la contraseña no coinciden.", sin el botón. En un `/entrar` limpio el botón tampoco aparece: solo se ve después de un 429 o de `magic_link_required`. Volver a invitarla tampoco sirve: el alta da 409 `already_member`, y sacarla y volver a sumarla no manda mail (F29). | media |

## Tanda 5: `make dev` de main 2dc077c (06/11/2026)

Levanté `make dev` con un directorio de datos nuevo. La web es un build de producción (`PORTAL_ENABLED=1 NEXT_PUBLIC_API_BASE=http://localhost:18080 next build`, `next start -p 13100`). La API se recorrió con los scripts de la tanda 4 y la web con Playwright a 390 px. A mitad de la tanda pasé el núcleo a un directorio en disco, porque /tmp está compartido, y por eso los contadores de intentos en memoria arrancaron de cero. Los usuarios nuevos son `*-sint.test`.

Una aclaración para leer los 429: en la primera mitad, los scripts de la API gastaron los cupos por IP documentados en el README de identity, que son 10 pedidos de link y 10 aceptaciones de invitación cada 15 minutos. Después de eso la web mostró "Hubo demasiados intentos" y no es una falla. Lo que sigue se repitió con los contadores en cero.

### Estado de las tandas 1 a 4

| # | Estado | Evidencia |
|---|--------|-----------|
| F1 | resuelta | La tanda 4 lo cubrió y no hubo cambios en esa zona. El replay de un `Idempotency-Key` con otro body da 422 `idempotency_key_reused`, también en `POST /v1/auth/magic-link`. |
| F2 | resuelta | Una factura con QR por el importe y la fecha del `PAGO PROVEEDOR EJEMPLO SA` pasa de 4 a 5 partidas (`source: document`). Después de `POST /v1/matching/runs` el pago ya no está en Excepciones. |
| F3 | resuelta | Galicia con `CLIENTE UNO` → `CLIENTE 1`: `quarantined, movement_conflict`, "line 11 has the date, amount, balance and references of a movement already stored, with another description". |
| F4 | resuelta | approver `other` con nota: 201. Aparece en el Sugerido del operador. operator `resolve`: 201, revision 2. operator `approve`: 403. |
| F5 | resuelta | Los 403 del viewer y del approver dejan filas `authz.denied` con `operation:getExport`, user_id, request_id, IP y `repeated`. |
| F6 | resuelta | viewer y approver: audit, movements y matches dan 403 "your role cannot download the tenant's data". |
| F7 | resuelta | El owner da accountant, owner, approver y viewer: 201. Si da operator, 403. |
| F8 | resuelta | Invitado sin contraseña: 200 en `invite/accept` y después entra con la contraseña nueva. En la web, ver F23. |
| F9 | resuelta | El Galicia subido a la cuenta Santander: `quarantined, bank_mismatch`. |
| F10 | resuelta | Seis contraseñas malas desde 127.0.0.2: la sexta da 429 con `magicLink: true`. El titular entra desde 127.0.0.3 con 200. Hubo 24 fallos repartidos entre 127.0.0.10 y 127.0.0.15, y después la contraseña buena desde 127.0.0.20 da 401 `magic_link_required`. |
| F11 | resuelta | `DELETE` del contador: 204. Después, su login y su cambio de empresa a alfa dan 403 `not_a_member`. Si el owner intenta sacarse a sí mismo siendo el último owner, da 409 `last_owner`. |
| F12 | resuelta | `GET /v1/ledger/files` trae cada archivo con su cuenta, su estado y el motivo. `GET /v1/ledger/movements?period=2026-10` da 200. |
| F13 | resuelta | El operator da accountant, operator, owner o approver: 403 en los cuatro. |
| F14 | sin repetir | Esta tanda no recorrió las decisiones del portal. |
| F15 | resuelta | `other` sin nota: 400 `note_required`. |
| F16 | resuelta | Una comilla suelta da `row_parse` "line 2: bare \" in non-quoted-field". Una comilla sin cerrar da "line 11: extraneous or missing \" in quoted-field". |
| F17 | resuelta | `period=2026-13` da "invalid parameter \"period\"". Un body sin `name` da "invalid request body: /name". |
| F18 | resuelta | El owner de beta con el `tenantId` de alfa: 403 `not_a_member`. |
| F19 | resuelta | `reject` sin nota, con espacios o con ZWSP: 400 `note_required`. |
| F20 | resuelta | owner, viewer y approver piden Sugerido, en matches y en pending: 403 en los seis casos. |
| F21 | resuelta | Factura con `nroDocRec` de beta: 201 `autoEligible: false`. |
| F22 | resuelta | Refresh por cookie con Origin `https://evil.example`, sin Origin, `null` o `http://localhost:13100.evil.example`: 403 `origin_not_allowed`. Con el origen de la web: 200. Lo mismo vale para `switch-tenant` por cookie. |
| F23 | resuelta | Invitación a 390 px: el link abre "Sumate a Tramo.", se acepta con 200 y entra a `/app` como "Inés Invitada · aprobador". Después de recargar sigue adentro. Si se reusa el link, aparece "La invitación ya se usó o venció…". Sin token, aparece "Este enlace está incompleto…". El link mágico entra a `/app`. El cambio de empresa funciona sin contraseña. |
| F24 | resuelta | Seis contraseñas malas desde la web y después la buena: "Hubo demasiados intentos. Probá de nuevo en 15 minutos." y el botón "Entrar con un enlace por mail". Hay `202` y `200 …/accept`, y entra a `/app`. Con `magic_link_required` la web muestra "Por seguridad, a esta cuenta hoy se entra con un enlace por mail." y el link también entra. |
| F25 | **sigue** | Un alta de alfa que da 409 `already_member`, con otras mayúsculas, cambia el email guardado a `CASING-…@ALFA-SINT.TEST`. Después el owner de beta suma a la misma persona con otras mayúsculas (201), y alfa pasa a verla como `Casing-…@Alfa-Sint.Test`. Con `APPROVER@ALFA.TEST` desde beta, el `/v1/me` del approver devuelve `APPROVER@ALFA.TEST`. |
| F26 | **sigue** | El owner de beta suma a `viewer@alfa.test`: 201, no hay ningún mail en el outbox y el viewer puede cambiar a beta (200). La decisión del 06/11 ("membership is always an accepted invitation") todavía no está en el código. |
| F27 | **sigue en parte** | Ahora los nombres de cuenta con ZWSP, RLO o relleno Hangul dan 400 `invalid_name`, con un mensaje claro. En cambio, `POST /v1/auth/register` acepta con 201 un `tenantName` de solo ZWSP, de solo relleno Hangul, de solo NBSP o con RLO. |
| F28 | resuelta | `reject` con nota de relleno Hangul, NBSP, solo RLO o solo un combinante: 400 `note_required`. Con texto real: 201. |
| F29 | **sigue** | Si a la persona la sacan antes de que acepte y abre la invitación, recibe 403, pero la contraseña queda guardada. Cuando la vuelven a sumar, entra con esa contraseña (200). La vuelta a sumar no manda mail: hay un solo mail de invitación y el token viejo da 401. |
| F30 | **sigue** | `POST /v1/tenant/members {"email":"owner​@alfa.test"}` (con ZWSP): 201, y crea un usuario nuevo. |
| F31 | **sigue** | Los mails siguen en inglés: "Subject: Your sign in link", "Sign in with this link…". |
| F32 | **resuelta** | Ahora `/entrar` siempre muestra "¿No tenés contraseña o no la recordás?…" con el botón "Entrar con un enlace por mail". La persona invitada que nunca abrió la invitación escribe su email y toca el botón. Hay `202` y aparece "Si ese email tiene cuenta en Tramo, te mandamos un enlace. Vale 15 minutos y sirve una sola vez.". El link del mail hace `200 …/accept` y entra a `/app` como "perdida-…@alfa-sint.test · aprobador", y al recargar sigue adentro. Si el email está vacío: "Escribí tu email arriba y te mandamos el enlace.", sin pedido a la API. Si el email no es válido: 400 y "Revisá el email: no parece una dirección válida.". Con un email que no existe: el mismo 202 y el mismo texto. Todo a 390 px, sin scroll horizontal. |

### Fallas nuevas

| # | Capa | Escenario | Pasos exactos | Esperado | Pasó | Sev. |
|---|------|-----------|---------------|----------|------|------|
| F33 | Web | Aceptar una invitación en un navegador donde ya entró otra persona le cambia la sesión sin avisar | 1) En un navegador, el owner entra por `/entrar` y abre `/app/aprobaciones`. 2) En otra pestaña del mismo navegador abre el link de invitación de `inv3-…@alfa-sint.test` (viewer), por ejemplo para ver qué le llega. 3) Completa nombre y contraseña y toca Entrar. 4) Vuelve a la pestaña del owner y navega. | Que la página de invitación avise "Estás adentro como owner@alfa.test" y pida salir primero, o que no toque la sesión abierta. | La invitación no menciona la sesión abierta. Después de aceptar, la pestaña del owner sigue mostrando "owner@alfa.test · titular" mientras navega sin recargar. En la próxima carga pasa a "Vale Visor · lectura" (`200 POST /v1/auth/refresh`), y en Descargas aparece "Tu rol no p…". El owner pierde su sesión sin enterarse, y durante un rato la pantalla dice una identidad mientras la cookie ya es de otra. | media |
| F34 | Web | El error de la invitación queda pegado | En `/invitacion#token=…`, 1) pone dos contraseñas distintas y toca Entrar: "Las dos contraseñas no coinciden.". 2) Las cambia por `123` en los dos campos y toca Entrar. | Que el aviso cambie a "Al menos 12 caracteres" o que se borre. | Sigue diciendo "Las dos contraseñas no coinciden." aunque son iguales. El navegador frena el envío por el largo mínimo y no hay pedido a la API, pero el texto en pantalla contradice lo que la persona escribió. | baja |
| F35 | API | El link mágico sale a la dirección como la escribió quien lo pidió | En `/entrar`, escribir ` PERDIDA-…@ALFA-SINT.TEST ` (con espacios y mayúsculas) y tocar "Entrar con un enlace por mail". | Que el mail salga a la dirección guardada de la cuenta (`perdida-…@alfa-sint.test`). | El outbox tiene `To: PERDIDA-…@ALFA-SINT.TEST`. Hoy el link llega igual, pero el destino lo decide el que pide y no la cuenta. Es la misma familia que F25. | baja |

### Lo que pasó bien en la tanda 5

| Escenario | Evidencia |
|-----------|-----------|
| Una carga por banco | Galicia, Santander, Macro y Mercado Pago: 201 `accepted` con 10, 10, 10 y 4 movimientos. Repetir Galicia: 200 `duplicate`. |
| Invitación a 390 px | Con nombre de solo ZWSP o con RLO: 400 y "Revisá el nombre: tiene que tener al menos una letra o un número…". El nombre es opcional según el spec (`AcceptInviteRequest` pide solo token y password): con el nombre vacío entra y el encabezado muestra el email. |
| Link mágico sin contraseña | Después de entrar por link, la invitación vieja sigue abriendo el formulario. Coincide con el spec: "works only while the person has no password". |
| Cambio de empresa a 390 px | El contador entra con contraseña y la pantalla pregunta "¿Con qué empresa querés trabajar?". Pasa de Alfa a Beta y de vuelta a Alfa sin pedir contraseña, con `200 switch-tenant`. En Beta, `/operador/bandejas`, `/operador/resultados`, `/app`, `/app/aprobaciones`, `/app/resultados` y `/app/descargas` no muestran ninguna cuenta de Alfa. Si vuelve dos veces para atrás, tampoco. En `/operador` aparece "Molino Alfa (dev)" en Clientes: es su lista de clientes, con el CUIT y los pasos que faltan, no datos de Alfa. Ninguna página tiene scroll horizontal y no hay 4xx. |
| Dos pestañas | Si la pestaña 2 cambia a Beta, la pestaña 1 sigue diciendo Alfa hasta que navega. En la próxima carga pasa a Beta. Es coherente con una sesión por navegador. |
| CORS | El preflight desde el origen de la web: 204 con credenciales. Desde las mayúsculas, la barra final, https, otro puerto, 127.0.0.1, `.evil.example`, `evil.localhost` o `null`: 403 sin ACAO. |
| Nombres de persona | `PATCH /v1/me` sigue rechazando las 16 variantes invisibles de la tanda 4. Con acentos y emoji: 200. |

### Notas

Después de mandar el link, el botón "Entrar con un enlace por mail" desaparece. Si el mail no llega, la persona tiene que recargar para pedirlo de nuevo. No lo cuento como falla, pero vale tenerlo en cuenta.

Por IP, los cupos son 10 pedidos de link y 10 aceptaciones de invitación cada 15 minutos. Ahora que el link es la vía normal para quien no tiene contraseña (F32), un estudio contable con una sola IP de salida puede llegar a ese techo en un día de altas.
