# Marco legal y ARCA para lo que automatizamos (investigación del 05/11/2026)

[H] = hecho con fuente leída. [I] = inferencia o práctica de mercado: verificar con contador o abogado. No es asesoramiento.

## Qué puede hacer la automatización

**Verde (con el contrato alcanza)**
- Emitir facturas y notas de crédito por web service (WSFEv1) con el servicio delegado al CUIT de Tramo. El cliente lo delega desde Administrador de Relaciones, eligiendo WebServices y cargando nuestro CUIT como representante; el certificado es el nuestro, el CUIT emisor es el del cliente. [H]
- Consultar padrón y constancia de inscripción, constatar comprobantes recibidos (WSCDCV1) y proveedores apócrifos (WSAPOC). [H]
- Procesar extractos y CSV que el cliente exporta (banco, Mercado Pago, Mis Comprobantes), leer facturas de proveedores recibidas por mail o PDF, conciliar, proponer asientos, preparar reportes, recordatorios de cobranza. [H]

**Amarillo (credenciales propias del cliente, cláusula en el contrato y aprobación humana)**
- Registrar comprobantes en el ERP del cliente, siempre con cola de aprobación y log.
- Operar "Mis Comprobantes" y Portal IVA: no tienen API. [H] El camino limpio es que el cliente delegue el servicio interactivo a una persona de Tramo con su propia clave fiscal nivel 3, como hacen los contadores, o que exporte el CSV. [H+I]
- Tokens de Mercado Pago o canal API de BBVA emitidos a nombre del cliente; agregadores como Prometeo con consentimiento escrito.
- Mandar datos de personas humanas (monotributistas, empleados) a Claude u OpenAI: con cláusulas y minimización (ver datos personales).
- Calcular retenciones y gestionar aceptación de Factura de Crédito Electrónica: el contador valida.

**Rojo (no se hace)**
- Guardar o usar la clave fiscal del cliente. ARCA la define como personal e intransferible y todo lo que pase es responsabilidad del cliente (RG 5048/2021). [H]
- Guardar o usar usuario y clave de home banking del cliente. Los términos de los bancos lo prohíben y el cliente pierde la cobertura por fraude (Banco Provincia, BPN, BIND). [H]
- Presentar declaraciones juradas (F.2051, IIBB) o firmar nada en nombre del contador. Registrar y proponer no es dictaminar; firmar sí está reservado por la Ley 20.488. [H+I]
- Ejecutar pagos o transferencias sin aprobación humana por operación.
- Registrar un proveedor nuevo sin constatar constancia y apócrifos.
- Tocar bases del cliente sin contrato de encargado firmado.

## Datos bancarios: hoy no hay API regulada
El Sistema de Finanzas Abiertas existe por decreto (353/2025) pero sin estándares; el BCRA dijo en octubre de 2026 que arranca en 2027 y con datos de ARCA, no cuentas bancarias. [H] BBVA tiene una API de conciliación solo para clientes empresa; Interbanking no publica API; Galicia, Santander, Macro y Bancor no tienen portal público que haya encontrado. Belvo no cubre Argentina; Prometeo dice cubrir el 60% de las cuentas pero su documentación está cerrada. [H]
Patrón seguro: el cliente exporta el extracto y lo sube, o nos da tokens de solo lectura a su nombre. [I]

## Datos personales (Ley 25.326)
- El cliente es responsable; Tramo es encargado (art. 25): no usar los datos para otro fin, no cederlos, destruirlos al cierre. [H]
- Inscribir las bases de Tramo en el RNBD por TAD: es gratis y la multa por no hacerlo es chica, la clausura no. [H]
- Medidas de seguridad recomendadas por la Res. 47/2018: accesos, cambios, backups, vulnerabilidades, destrucción, incidentes. [H]
- **Estados Unidos no es país con protección adecuada.** Para usar Claude u OpenAI hacen falta cláusulas modelo (Disp. 60-E/2016 o Res. 198/2023) o consentimiento, más el DPA del proveedor firmado y minimización: no mandar nombres, CUIL ni sueldos si la tarea no lo necesita. [H+I]
- Hay un proyecto de ley nuevo (3397-D-2026) sin tratamiento; rige la 25.326. [H]

## Responsabilidad
La responsabilidad fiscal es siempre del contribuyente (Ley 11.683): un error de software no lo exime, y nuestros logs son la prueba de qué aprobó una persona. Los comprobantes se conservan 10 años y los soportes informáticos operativos 2 años desde el cierre. [H] Multas formales de $150.000 a $2.500.000 desde la Ley 27.799. [H]

Cláusulas del contrato [I]: objeto limitado a proponer y preparar, nunca declarar; aprobación humana obligatoria antes de cualquier efecto en ARCA, banco o pago; el cliente sigue siendo contribuyente y responsable de datos y su contador conserva sus incumbencias; anexo de encargado con subencargados nombrados (Anthropic, OpenAI, cloud) y transferencia internacional; política de credenciales (delegaciones y tokens, nunca contraseñas); logs conservados 10 años o entregados al cierre; límite de responsabilidad (por ejemplo honorarios de 12 meses); deber del cliente de validar en Portal IVA antes del vencimiento.

## Sorpresas con fecha
1. **1/12/2026**: ARCA rechaza pedidos de CAE sin la condición frente al IVA del receptor (RG 5616/2024). Si emitimos facturas por WS, el campo tiene que viajar. [H]
2. Desde noviembre de 2025 el contribuyente valida los comprobantes recibidos en el Portal IVA (IVA Simple, RG 5705/2025): lo que registramos tiene que cerrar con lo que el contador valida. [H]
3. El manual de web services va por la versión 4.5 (julio 2026) y cambia seguido. [H]
4. Factura de Crédito Electrónica: plazo de 21 días hasta el 31/10/2026; qué rige después no está confirmado. Importa si el molino recibe FCE de proveedores MiPyME. [H]
5. RG 5893/2026 y 5866/2026 ampliaron los obligados a factura electrónica. [H]

## Checklist antes del primer cliente pago
1. Certificado digital de Tramo (homologación y producción) y una emisión real por WSFEv1 con el CUIT de un cliente tras la delegación.
2. Procedimiento escrito de delegación para el cliente y de revocación al cierre.
3. Campo "condición IVA del receptor" implementado y probado antes del 1/12/2026.
4. Contrato de servicio con las cláusulas de arriba.
5. Anexo de encargado de tratamiento; DPA de Anthropic y OpenAI firmados; cláusulas modelo archivadas.
6. Bases de Tramo inscriptas en el RNBD.
7. Política de seguridad mínima mapeada a la Res. 47/2018 (MFA, backups, logs, incidentes, destrucción).
8. Política de credenciales: nunca claves fiscales ni de home banking; bóveda cifrada para tokens.
9. Flujo de cuentas a pagar con verificación de proveedor y cola de aprobación antes de escribir en el ERP.
10. Broker por seguro de responsabilidad profesional y ciber; acuerdo con el contador de cada cliente sobre quién valida Portal IVA y retenciones cada mes.

## Checklist del contrato con el cliente (06/11/2026)

Lo que el contrato tiene que decir, punto por punto. Es la lista para el abogado, no el texto. Todo es [I] salvo lo marcado.

1. **Partes y representación**: razón social y CUIT de las dos partes; quién firma por el cliente y con qué poder.
2. **Objeto**: proponer, preparar y registrar en los sistemas del cliente. Nunca declarar, presentar ni firmar ante ARCA o rentas, ni mover plata. [H+I: Ley 20.488 para lo reservado al contador]
3. **Anexo de alcance por proceso**: qué corre solo, qué pasa por Sugerido y Excepciones, umbrales iniciales y quién del cliente aprueba. Sin anexo firmado no se arranca.
4. **Aprobación humana**: nada tiene efecto en ARCA, banco, pago o ERP sin la aprobación que fije el anexo. Las decisiones quedan en el audit log y ese log es la prueba de quién aprobó qué.
5. **Obligaciones del cliente**: delegar en ARCA lo que contrate, mandar los extractos a tiempo, decidir sus excepciones, mantener vigentes las credenciales de su ERP y validar en Portal IVA antes de cada vencimiento con su contador.
6. **Responsabilidad fiscal**: el cliente sigue siendo el contribuyente y el responsable de sus datos; su contador conserva sus incumbencias. [H: Ley 11.683]
7. **Política de credenciales**: solo delegaciones, tokens a nombre del cliente, claves de API revocables y exportes. Nunca la clave fiscal ni el usuario y la clave del home banking. Si el cliente las manda igual, se borran sin usarse y se le avisa. [H: RG 5048/2021 y los términos de los bancos]
8. **Servicio y soporte**: respuesta en 4 horas hábiles y arreglos menores en 2 días hábiles. Excluidas las caídas de ARCA, del banco o del ERP del cliente.
9. **Precio**: build por proceso más fee mensual; moneda, ajuste y qué pasa si se paga tarde. Las herramientas de terceros van a costo o en cuentas del cliente.
10. **Plazo y salida**: mensual, cancelable con 30 días de aviso. Al irse: exporte completo, revocación de delegaciones y tokens, borrado de lo nuestro en un plazo fijo, salvo lo que la ley obliga a conservar.
11. **Propiedad**: los datos y la configuración son del cliente; el software se licencia. Pendiente de decisión (ver `product/ESPECIFICACION.md`, sección 9).
12. **Uso de los datos**: solo para prestar el servicio. No se usan para entrenar modelos, ni de Tramo ni de terceros.
13. **Confidencialidad**: alcanza a las dos partes y sobrevive al contrato.
14. **Límite de responsabilidad**: por ejemplo, los honorarios de 12 meses. Exclusiones: dolo y culpa grave.
15. **Anexo de encargado de tratamiento** (checklist de abajo), firmado junto con el contrato.
16. **Jurisdicción, ley aplicable y firma**: definir los tribunales y si alcanza con firma electrónica.

## Checklist del anexo de encargado (DPA)

El cliente es el responsable y Tramo, el encargado. [H: Ley 25.326, art. 25] Lo marcado [I] se confirma con el abogado.

1. **Finalidad**: tratar los datos solo para el servicio del contrato y solo por instrucción documentada del cliente. [H: art. 25]
2. **Datos y titulares**: CUIT, razón social y domicilio de clientes y proveedores; movimientos bancarios y de Mercado Pago; CBU; comprobantes; mails y nombres de los usuarios del portal. Los titulares incluyen personas humanas: monotributistas, proveedores unipersonales y empleados si aparecen en extractos.
3. **Lo que no se trata**: claves fiscales, claves de home banking, datos sensibles del art. 2. Ningún campo del modelo de datos puede guardarlos.
4. **Confidencialidad del personal**: compromiso escrito de cada persona de Tramo con acceso. [H: art. 10]
5. **Medidas de seguridad**, cada una con lo que hacemos (`eng/ARQUITECTURA.md` §5 y §6) y mapeadas a la Res. 47/2018 [H]:
   - Accesos: roles por tenant, segundo factor y RLS.
   - Cambios: CI y audit log append-only.
   - Respaldo: backups diarios cifrados, con una restauración probada por mes.
   - Vulnerabilidades: gosec y govulncheck en CI.
   - Cifrado: claves por tenant.
   - Destrucción: el procedimiento del punto 11.
   - Incidentes: el punto 8.
6. **Subencargados nombrados**: el proveedor del VPS, el bucket de backups, el proveedor de mail y los modelos de lenguaje (Anthropic, OpenAI si se usa). Aviso al cliente con 30 días antes de sumar uno, con derecho a oponerse. [I]
7. **Transferencia internacional**: región de la base, decidida y escrita. Para Estados Unidos (modelos de lenguaje) van cláusulas modelo y el DPA del proveedor firmado y archivado. A los modelos se manda lo mínimo: sin nombres de personas, CUIL ni sueldos si la tarea no los necesita. [H: Disp. 60-E/2016 y Res. 198/2023; minimización: I]
8. **Incidentes**: aviso al cliente sin demora y con un plazo fijo, por ejemplo 48 horas desde que se detecta. Qué pasó, qué datos, qué se hizo. La ley no fija el plazo. [I]
9. **Derechos de los titulares**: Tramo le pasa al cliente cualquier pedido que le llegue y lo ayuda a contestar a tiempo. Acceso: 10 días corridos. Rectificación y supresión: 5 días hábiles. [H: arts. 14 y 16, verificar]
10. **Auditoría**: el cliente puede pedir evidencia de las medidas una vez por año, o después de un incidente.
11. **Fin del contrato**: devolver todo en el formato de "Descargar todo" del portal y destruir lo nuestro en un plazo fijo, con constancia escrita. Excepción: lo que la ley obliga a conservar (comprobantes 10 años, soportes 2 años), y en ese caso se dice quién lo conserva. [H: Ley 11.683 para los plazos]
12. **Registro**: las bases de Tramo inscriptas en el RNBD antes del primer cliente. [H]

## Fuentes principales
ARCA catálogo de WS: arca.gob.ar/ws/documentacion/catalogo.asp · Guía de delegación: afip.gob.ar/guias/paso-a-paso · RG 5048/2021 · RG 5616/2024 (iprofesional.com/impuestos/464104) · RG 5705 y 5707/2025 (blogdelcontador.com.ar) · Decreto 353/2025 y nota del BCRA de octubre 2026 (elliberal.com.ar/nota/94159) · BBVA API Market (bbvaapimarket.com) · TyC Banco Provincia (bancoprovincia.com.ar/CDN/Get/TYC_BIP) · Ley 25.326 (infoleg) · Res. 126/2024 y 47/2018 AAIP · Disp. 60-E/2016 y Res. 198/2023 (marval.com) · DPA de Anthropic (anthropic.com/legal/data-processing-addendum) · Ley 11.683 (infoleg) · Ley 27.799.

Lagunas sin fuente: APIs de Galicia, Santander, Macro y Bancor; lista de bancos de Prometeo en Argentina; política de datos de OpenAI (no respondió); habilitación municipal en el sur de Córdoba; plazo de FCE desde noviembre 2026.
