# Web services de ARCA: lo que hay que saber para implementar (05/11/2026)

Sacado de los manuales oficiales (texto extraído en `manuales/`). Lo marcado [I] es inferencia.

## Autenticación (WSAA)
1. Armar el TRA: XML `loginTicketRequest` con `uniqueId`, `generationTime`, `expirationTime` y `service` (por ejemplo `wsfe`).
2. Firmarlo como CMS (PKCS#7) con el certificado y la clave privada; base64.
3. Llamar `loginCms` → devuelve `token` y `sign`, válidos **12 horas**.
4. Cada llamada a un servicio lleva `Auth{Token, Sign, Cuit}`; el `Cuit` es el del cliente representado y tiene que estar en la lista `relations` del token.
- Endpoints: homologación `wsaahomo.afip.gov.ar/ws/services/LoginCms`, producción `wsaa.afip.gov.ar/ws/services/LoginCms`.
- **Un certificado de Tramo sirve para todos los clientes**: cada cliente delega el servicio a nuestro CUIT y el token trae las relaciones. Un TA por servicio, compartido entre clientes y entre workers, con lock: pedir dos TA del mismo servicio con un TA vigente da `coe.alreadyAuthenticated`.
- Reloj: NTP contra `time.afip.gov.ar`, GMT-3, `generationTime` unos minutos atrás, `expirationTime` 10 a 20 minutos adelante. Tolerancia de 24 horas.
- Certificado de producción: clave fiscal nivel 3 → "Administración de Certificados Digitales" → alias → subir CSR. CSR con `serialNumber=CUIT <cuit>`, `O=<razón social>`, `C=AR`. La validez en años no figura en ningún manual oficial (la comunidad dice 2).

## Factura electrónica (WSFEv1, servicio `wsfe`)
- Manual v4.8 del 01/12/2026: arca.gob.ar/fe/ayuda/documentos/wsfev1-RG-4291.pdf. Endpoints `wswhomo.afip.gov.ar/wsfev1/service.asmx` y `servicios1.afip.gov.ar/wsfev1/service.asmx`.
- Métodos: `FECAESolicitar`, `FECompUltimoAutorizado(PtoVta, CbteTipo)`, `FECompConsultar`, `FECompTotXRequest`, los `FEParamGet*` (tipos de comprobante, concepto, documento, IVA, monedas, tributos, puntos de venta, cotización) y `FEParamGetCondicionIvaReceptor`.
- `FECAESolicitar`: cabecera `{CantReg, PtoVta, CbteTipo}` y detalle con `Concepto, DocTipo, DocNro, CbteDesde, CbteHasta, CbteFch, ImpTotal, ImpNeto, ImpIVA, ImpTrib, MonId, MonCotiz, CondicionIVAReceptorId, Iva[], Tributos[], CbtesAsoc[], Opcionales[]`. Devuelve `CAE`, `CAEFchVto`, `Resultado` (A, R, P), observaciones y errores.
- **`CondicionIVAReceptorId` obligatorio desde el 01/12/2026** (RG 5616). Códigos: 1 responsable inscripto, 4 exento, 5 consumidor final, 6 monotributo, 7 no categorizado, 8 proveedor del exterior, 9 cliente del exterior, 10 liberado ley 19.640, 13 monotributista social, 15 no alcanzado, 16 monotributo promovido. Valor inválido: CAE rechazado.
- Numeración: `CbteDesde` tiene que ser el último autorizado más uno (error 10016). Fecha: hasta 5 días atrás para concepto 1, nunca de otro mes.
- Errores que delatan la delegación incompleta: 600 "no se corresponden token y firma", 601 "CUIT representada no incluida en token".
- Factura de Crédito Electrónica MiPyME: mismos métodos, tipos 201 a 213, opcional 2101 = CBU obligatorio, `FchVtoPago` obligatorio en facturas; aceptación y rechazo van por otro servicio, `wsfecred`.
- WSMTXCA: igual pero con detalle de ítems. WSFEX: exportación.

## Consultas (con el CUIT propio de Tramo, sin delegación por cliente)
- **WSCDC** (`wscdc`): `ComprobanteConstatar(CuitEmisor, PtoVta, CbteTipo, CbteNro, CbteFch, ImpTotal, CodAutorizacion)` → aprobado o rechazado. Para validar cada factura de proveedor antes de registrarla. Manual v0.4: afip.gob.ar/ws/WSCDCV1/WSCDC-manual-desarrollador-v4.pdf.
- **ws_sr_constancia_inscripcion** (ex padrón a5): `getPersona_v2(cuitRepresentada, idPersona)` → razón social, domicilio fiscal, estado de la clave, impuestos, regímenes, actividades, datos de monotributo. De acá sale la condición frente al IVA del receptor. Manual v4.1 de marzo 2026.
- **ws_sr_padron_a13**: datos de identidad y `getIdPersonaListByDocumento` (DNI a CUIT).
- **WSAPOC** (`wsapoc`): base de facturas apócrifas, `GetPublicacionAPOC(cuit)`. Para frenar proveedores antes de registrarlos.
- Padrón a4 y a10 son para organismos, no para nosotros.
- Cada uno se habilita una vez para nuestro computador fiscal en Administrador de Relaciones.

## Lo que NO tiene web service (se resuelve con exportes)
- **Mis Comprobantes** (emitidos y recibidos): grilla del portal con exporte CSV o XLS, filtrable por período. Columnas: fecha, tipo, punto de venta, número, código de autorización, tipo y número de documento del emisor, denominación, tipo de cambio, moneda, neto gravado, no gravado, exentas, otros tributos, IVA, total. AfipSDK y otros lo scrapean con navegador y clave fiscal: no lo hacemos con la clave del cliente (ver ops/LEGAL.md).
- **Portal IVA, Libro IVA Digital, IVA Simple**: portal con importación y exportación CSV o TXT.
- **Mis Retenciones**: portal con exporte. **SIRCREB**: Comisión Arbitral, por SIFERE Web. **e-cheq**: dominio del banco.
- **El QR de cada factura** (RG 4291): `https://www.arca.gob.ar/fe/qr/?p=<base64 JSON>` con `ver, fecha, cuit, ptoVta, tipoCmp, nroCmp, importe, moneda, ctz, tipoDocRec, nroDocRec, tipoCodAut (E o A), codAut`. Se decodifica con cualquier lector de QR y se verifica con WSCDC. Especificación: afip.gob.ar/fe/qr/documentos/QRespecificaciones.pdf.

## Librerías
- `pyafipws` (Python, LGPL, último push julio 2025, release de PyPI de 2016): cubre todo pero es código viejo; instalar desde git.
- `@afipsdk/afip.js`, `afip.py`, `afip.php` (MIT, activas): **toda llamada y el certificado pasan por los servidores de AfipSDK** (app.afipsdk.com). Precio por cantidad de CUITs (Pro USD 25 por mes y 10 CUITs). Para un producto que maneja datos fiscales de terceros, no conviene: un tercero custodia certificados y claves. [I]
- Recomendación: cliente SOAP propio y fino. En TypeScript, `soap` o fetch con plantillas y firma CMS con `node-forge` u `openssl`; en Python, `zeep` más `cryptography`. Son unas ocho llamadas y los manuales son precisos. Para CI, `EasyIndustry/arca-api-sim` (simulador local de WSAA y WSFEv1). Referencia self-hosted: `NehuenLian/AFRelay` (FastAPI sobre WSAA y WSFEv1, MIT).

## Homologación
- Se gestiona en WSASS (arca.gob.ar/ws/WSASS): certificado de prueba con alias y CSR, y "Crear Autorización a Servicio" con el CUIT representado, que permite simular la delegación.
- No hay CUITs de prueba publicados: se prueba con el propio en el padrón de homologación. `FEParamGetPtosVenta` no devuelve nada en homologación: usar punto de venta 1. La numeración sigue desde `FECompUltimoAutorizado`. `FEDummy` puede decir OK mientras otras cosas fallan.
- Contactos: webservices-desa@arca.gob.ar (pruebas), sri@arca.gob.ar (producción).

## Checklist de delegación por cliente
1. El cliente crea el punto de venta "RECE para aplicativo y web services" en "Administración de puntos de venta y domicilios".
2. El cliente, con clave fiscal nivel 3, en Administrador de Relaciones: Nueva Relación → su empresa → ARCA › WebServices › Facturación Electrónica → Representante: el CUIT de Tramo → confirma (F.3283/E).
3. Tramo acepta la designación y crea la relación con su computador fiscal (alias). No se puede subdelegar.
4. Prueba: TA de `wsfe` y `FECompUltimoAutorizado(PtoVta, 6)` con el CUIT del cliente. Error 600 o 601 es cadena incompleta.
5. Guardar CUIT, punto de venta, condición de IVA (de la constancia) y el F.3283/E.

## La integración mínima del MVP (6 llamadas)
1. `loginCms` para `wsfe`, `wscdc` y `ws_sr_constancia_inscripcion`.
2. `getPersona_v2` para resolver cada CUIT de proveedor o cliente.
3. `ComprobanteConstatar` para validar cada factura de compra antes de registrarla.
4. `FECompUltimoAutorizado` y 5. `FECAESolicitar` para emitir a nombre de clientes.
6. `FECompConsultar` para reconciliar y reintentar después de un timeout (nunca repetir un pedido de CAE sin releer el último autorizado).
Los catálogos `FEParamGet*` se cargan una vez por día.

## Operación
- Un almacén de TA compartido, un solo solicitante por servicio, refresco al vencimiento menos margen.
- Renovación de certificado: nuevo CSR bajo el mismo alias conserva las delegaciones. [I] Revocación: el cliente la hace desde su Administrador de Relaciones.
- ARCA no publica ventanas de mantenimiento ni página de estado. Reintentos con backoff y emisión idempotente.
