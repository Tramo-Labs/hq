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

## Sin probar todavía

El cierre de período (`period_closed`): no hay endpoint para cerrar un período, así que el cliente no puede llegar a ese estado. La factura con QR dentro de una imagen o en la capa de texto (probé solo el link). El gateway ARCA desde la API, que el núcleo no usa en ningún endpoint del cliente. El tope de 20 fallos por hora por email desde varias IPs: el lead pidió no repetir F10.
