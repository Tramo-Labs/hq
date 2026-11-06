# Decisiones (append-only)

| # | Fecha | Decisión | Por qué |
|---|-------|----------|---------|
| D-1 | 2026-11-05 | Servicios primero (automatizaciones con IA para back office), producto después | Factura sin construir; el dashboard de resultados se hace con el primer cliente |
| D-2 | 2026-11-05 | Código propio desde cero; el monorepo anterior solo como inspiración de estructura | Es propiedad de otra empresa |
| D-3 | 2026-11-05 | Ningún número publicado sin medir | Misma regla que funcionó antes; evita vender humo |
| D-4 | 2026-11-05 | Nombre provisorio Tramo; la organización de GitHub sigue siendo Lionware-Solutions | Martín quiere ver la página antes de decidir el nombre |
| D-5 | 2026-11-05 | Vertical: pymes de alimentos y agro del sur de Córdoba, back office financiero; modelo build fijo más fee mensual | Es el hueco que la competencia no ocupa y donde ya hay dos casos reales |
| D-6 | 2026-11-05 | La oferta es "tu caja cerrada y controlada cada 15 días", no "conciliación" | Nadie paga la conciliación suelta; el competidor real es el junior del contador y el Excel |
| D-7 | 2026-11-05 | Núcleo del producto propio, no n8n; cliente SOAP de ARCA propio, no AfipSDK | El matching necesita reglas versionadas y tests; un tercero no custodia certificados ni datos fiscales de clientes |
| D-8 | 2026-11-05 | Nunca claves fiscales ni de home banking de clientes; solo delegaciones, tokens y exportes | ARCA y los bancos lo prohíben y el cliente pierde cobertura |
| D-9 | 2026-11-05 | Deploy de la landing por GitHub Actions, no por la integración de Vercel | El plan Hobby no acepta repos privados de organización |
| D-10 | 2026-11-06 | Núcleo en Go: monolito modular `core` (api y worker) más `arca-gateway` separado; gRPC entre ellos, SOAP solo dentro del gateway | Un deploy para dos personas; la clave de ARCA vive en un solo proceso |
| D-11 | 2026-11-06 | Postgres único, multi-tenant con `tenant_id` y RLS desde la migración cero | El software se vende entero: cada comprador es un tenant o un despliegue idéntico |
| D-12 | 2026-11-06 | Contrato primero: OpenAPI 3.1 con Swagger en `/docs`, proto para gRPC; `Idempotency-Key` obligatoria en todo POST que crea | Cualquier frontend o integrador consume el contrato; los reintentos nunca duplican |
| D-13 | 2026-11-06 | Sesión con JWT EdDSA de 10 minutos más refresh opaco rotado por familia; argon2id; TOTP para quien aprueba pagos | Robo de refresh detectado y revocado; nada reutilizable a largo plazo |
| D-14 | 2026-11-06 | Cifrado de sobre por tenant para certificados, tokens OAuth, claves de ERP y extractos; audit log append-only; backups cifrados probados por mes | Ley 25.326 y 11.683; la clave maestra nunca está en la base |
| D-15 | 2026-11-06 | Infra: imágenes en GHCR por commit, compose en un VPS con Caddy, Terraform desde el día uno aunque sea una máquina | El mismo repo levanta un ambiente para un cliente que compre el software |
| D-16 | 2026-11-06 | River (cola de jobs sobre Postgres, licencia MPL-2.0) se usa sin modificar sus archivos | MPL-2.0 es compatible con vender el software entero mientras no se toquen los archivos de la librería |
| D-17 | 2026-11-06 | Límite de login por IP y por par IP y email, nunca bloqueo global por email; con muchos fallos desde distintas IPs se exige link mágico | Un atacante no puede dejar afuera al dueño; el dueño siempre tiene salida |
| D-18 | 2026-11-06 | El portal y la consola llaman a la API del núcleo directo desde el navegador (CORS), sin pasar por el servidor de Next; web y API son subdominios del mismo dominio registrable (app.X y api.X, siempre https) o la cookie de refresh no viaja; la landing sigue en Vercel | El núcleo ve la IP real detrás de su propio Caddy; Vercel no tiene direcciones fijas en las que confiar |
| D-19 | 2026-11-06 | Sumar a alguien a una empresa es siempre una invitación que la persona acepta con un link de un solo uso; hasta aceptar, la membresía está pendiente y no da acceso ni modifica su cuenta. Los mails del producto van en castellano | Nadie entra a una empresa sin saberlo (F26); una empresa nunca escribe datos de la cuenta de un usuario ajeno (F25) |
| D-20 | 2026-11-06 | Los límites por IP cuentan solo fallos (token inválido, vencido, contraseña mala), nunca aceptaciones ni logins correctos; el preview de una invitación válida no cuenta | Una oficina entera entra desde la misma IP el mismo día (F40); el atacante sigue frenado porque solo falla |
| D-21 | 2026-11-06 | El portal del cliente y la consola del operador son una app aparte (`eng/app`, deploy propio en app.X); la landing queda sola en `eng/web` | Pedido de Martín: son productos distintos con ritmos y riesgos distintos; la landing nunca debe arrastrar el portal |
