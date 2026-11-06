# El producto: Tramo Conciliación y Cuentas a Pagar (investigación del 05/11/2026)

Hechos con fuente; lo marcado [I] es inferencia. Ninguna cifra de precisión de la competencia está verificada por terceros: todas son marketing del proveedor.

## Qué hay en el mercado, uno por uno
- **Xubio** (Visma): conciliación dentro de su ERP. Entrada por PDF con IA (lanzado en octubre 2026, gratis hasta el 31/01/2027, máximo 3 por mes), Excel de un solo formato con tope de 50 KB, o Interbanking (requiere plan pago del cliente y trámite bancario de 1 a 3 días). Matching por fecha y monto con tolerancias escalonadas guardadas en el navegador, sin aprendizaje, sin muchos a uno salvo etiquetas. Mercado Pago no entra a la conciliación. Desde $161.800 más IVA por mes (plan Empresa Estándar). Solo sirve si la contabilidad está en Xubio.
- **Colppy**: conciliación manual de dos paneles. "Conectá tu banco" ingresa con el usuario y clave del home banking del cliente (scraping), se cae con tokens y claves vencidas, sin bancos con doble factor. CSV de 100 filas por importación. Desde $189.500 más IVA.
- **Tango** (Axoft): el usuario arma el formato de extracto por banco; matching "igual a" o "contiene" sin tolerancias; Tango Banking solo por Interbanking y Galicia con licencia Plus. Sin IA.
- **Contabilium**: importa movimientos bancarios pero no tiene pantalla de conciliación. Sin escritura por API.
- **Asentia** (Velvok, España): el mejor motor publicado del segmento pyme: nueve reglas deterministas (cheque por número, tolerancia ±15 días, redondeo hasta $10, combinaciones muchos a uno y uno a muchos con tope de 6 ítems, agrupación por concepto como SIRCREB, reglas del cliente, Rapifast y Banelco netos de comisión) y un LLM solo sobre lo que sobra, con criterios aprendidos por cuenta y modo sombra. Pero: solo archivos (sin banco, sin Interbanking, sin Mercado Pago), exporta plantillas para 12 sistemas que el usuario importa a mano, sin precios, sin clientes nombrados, equipo chico y reciente. Afirma 92% automático.
- **Looking for Trouble**: agentes a medida operados, fee mensual por agente sin monto. Agente de conciliación (46 bancos, reglas y umbrales, tesorería aprueba ajustes) y de facturas (CAE, lista de proveedores, umbral de aprobación). Todos los números de su sitio están marcados "ejemplos ilustrativos"; ningún caso atribuido.
- **CargaFacturas**: cuentas a pagar con OCR, CAE verificado contra ARCA en tiempo real, cadenas de aprobación, 96% de precisión declarada. Sin lado bancario.
- **The Eye** y **Conciliac**: enterprise (Quilmes, Comafi, Kavak), Interbanking y APIs bancarias, similitud de descripción, 95% o más declarado. No es producto para pymes.
- Otros: Conciliame (SaaS chico), Cedalio (cuentas a pagar para 200 a 5.000 empleados, USD 1 a 10 por factura), Nubceo (liquidaciones de tarjetas), Frisvy (de Interbanking), SpomBridge (ERP con OAuth de Credicoop), Duotach (consultora; su post trae seis casos de prueba que vale usar como suite).

## Cómo entran los movimientos bancarios en Argentina
| Vía | Realidad hoy |
|---|---|
| Archivo exportado del home banking (Excel, CSV, TXT) | Universal. Formatos distintos por banco, sin especificación pública: hay que catalogarlos con cuentas reales. Es trabajo pesado y es una barrera para los demás. |
| PDF con IA u OCR | Ya es estándar (Xubio, Asentia, The Eye, LFT). |
| Mercado Pago | **La única API limpia y documentada**: OAuth, reportes de Liquidaciones y Dinero en cuenta por API, 60 días por reporte, SFTP y webhook, con comisiones e impuestos separados. |
| Interbanking | 58 bancos, consultas ilimitadas en todos los planes, API en los planes pagos, sin documentación pública, alta por empresa de 1 a 3 días. Es de donde tiran Xubio, Tango, The Eye y Conciliac. |
| Agregadores | Belvo y Pluggy no cubren Argentina. Prometeo tiene la documentación cerrada. |
| APIs de bancos | Credicoop API Empresas por OAuth es la única de nivel pyme encontrada. Los grandes solo por host to host corporativo. |
| Regulación | Sin API regulada antes de 2027. |

## Escritura en el ERP del cliente
- **Xubio**: API con asiento manual, comprobante de compra y pago (OAuth, un token por empresa, sin sandbox). No lee movimientos ni marca conciliado, pero su autoconciliación cierra sola por fecha y monto.
- **Odoo** y **Alegra**: ciclo completo, incluso marcar conciliado. **SAP B1**: completo, para cuando aparezca un cliente mediano.
- **Tango Delta**: solo asientos por API con token por cliente; compras por archivo.
- **Colppy**: API sin verificar (documentación con registro). **Contabilium**: sin escritura.
- Conclusión: archivos de importación en la plantilla exacta de cada ERP desde el día uno; API donde exista.

## El producto que compite
**La apuesta** [I]: un servicio operado donde (a) todos los movimientos de banco y Mercado Pago entran sin que la pyme toque una plantilla, (b) la tasa de conciliación automática se mide y se publica por cliente, (c) el asiento cae en el ERP que ya usan, y (d) el dueño lee el panel en un minuto. La pyme no compra matching: compra "mi conciliación y mis facturas de proveedores están al día y alguien responde si no".

### Lo imprescindible
1. Entrada sin plantillas: PDF (incluso escaneado) y cualquier exporte de Galicia, Santander, BBVA, Macro, Nación, Credicoop y Bancor, con parsers mantenidos por nosotros y un LLM de respaldo que mapea formatos nuevos y pide confirmación una vez. Control de saldos (inicial más créditos menos débitos igual a final) antes de correr nada. Deduplicación por hash de archivo y por movimiento.
2. Mercado Pago como cuenta de primera clase: OAuth, reportes programados, comisiones separadas para que el neto cierre con el depósito en el banco.
3. Lado contable: ítems abiertos del ERP por API (Xubio, Odoo, Alegra) o el mayor de banco exportado (Tango, Colppy, Contabilium).
4. Motor de matching determinista, en pasadas ordenadas: referencia única (número de cheque, id de Mercado Pago, id de transferencia, número de factura en la descripción); monto exacto con ventana de fechas; tolerancia de monto con tope absoluto, generando la línea de ajuste documentada; uno a muchos y muchos a uno con topes; plantillas de "neto de comisión" por banco (Mercado Pago, Getnet, Payway, Banelco, Rapifast, SIRCREB, IIBB, comisiones más IVA); reglas del cliente. Dos candidatos iguales es pendiente, nunca una adivinanza. Cada match guarda la regla que lo hizo.
5. LLM solo donde las reglas paran: normalizar descripciones a contrapartes (CUIT, nombre), proponer contraparte para lo que sobra con confianza, y escribir una explicación en castellano de cada excepción. Las propuestas nunca se registran solas: van a la cola.
6. Criterios aprendidos por cliente: cada decisión humana es candidata a regla, en modo sombra antes de aplicarse sola.
7. Cuentas a pagar: casilla dedicada (WhatsApp después), separación de facturas de otros adjuntos, extracción con confianza por campo, CUIT contra padrón y CAE contra ARCA, duplicados, lista de proveedores autorizados, orden de compra opcional, alta del comprobante de compra en el ERP o archivo de importación.
8. Paquete de cierre mensual: conciliado, pendiente por motivo, ajustes, puente entre saldo bancario y contable. Es lo que pide el contador.

### El flujo de aprobación
Tres bandejas: **Automático** (regla exacta sin ambigüedad: se registra con etiqueta y se revisa después), **Sugerido** (tolerancia, propuesta del LLM, combinaciones, cualquier ajuste: lo decide un operador de Tramo) y **Excepciones** (sin candidato, duplicados, saldo que no cierra, factura sin orden o sobre el umbral: las decide el cliente con la explicación y un toque: aprobar, rechazar, "es otra cosa"). Umbrales por cliente. Cierre semanal del operador, resumen al cliente el lunes, paquete mensual. Nada mueve plata: Tramo nunca tiene permisos de pago.

### El panel
Tasa de conciliación automática por cuenta y por mes, tasa de sugerencias aceptadas, excepciones y antigüedad, pendientes por motivo, diferencia banco contra contabilidad hoy, días hasta el cierre, facturas procesadas y retenidas, duplicados atrapados, horas ahorradas contra una línea base que el cliente confirma al arrancar, y qué reglas disparan. La curva de automatización de cada cliente es el material de venta del siguiente.

### Orden de integraciones
1. Archivos de los siete bancos más PDF con IA. 2. Mercado Pago por OAuth. 3. Xubio por API. 4. Odoo y Alegra (ciclo completo). 5. Tango (asientos por API, compras por archivo). 6. Colppy y Contabilium por archivo. 7. Interbanking y Credicoop cuando haya camino comercial para un integrador. 8. SAP B1 con el primer cliente mediano.

### Las 12 semanas de construcción
- Semanas 1 y 2: modelo de datos; parsers de los siete bancos con exportes reales de tres pymes de diseño; PDF con confianza por campo; control de saldos; deduplicación.
- Semanas 3 y 4: motor pasadas 1 a 3, regla guardada en cada match, cola de pendientes con motivo, suite de prueba con los seis casos de Duotach más 200 movimientos etiquetados a mano por pyme. Entregable: tasa de automatización medida por pyme.
- Semanas 5 y 6: combinaciones, plantillas de neto de comisión, Mercado Pago de punta a punta.
- Semanas 7 y 8: consola del operador, página de aprobación del cliente, LLM para normalización y explicaciones, decisiones a reglas en sombra. Entregable: cierre quincenal operado para las tres pymes.
- Semanas 9 y 10: escritura en Xubio por API y plantillas de Tango, Colppy y Contabilium; cuentas a pagar de la casilla al ERP con cola de revisión.
- Semanas 11 y 12: panel, paquete de cierre, audit log, roles (operador, aprobador, contador de solo lectura), política de retención y cifrado escrita, checklist de alta. Entregable: tres pymes en piloto pago con su número publicado.

### Stack
Núcleo como servicio propio, no n8n: el matching necesita pasadas ordenadas, combinatoria con topes, reejecuciones idempotentes, reglas versionadas con modo sombra, log de decisiones y métricas por regla. Python o TypeScript, Postgres, un runner de trabajos, almacenamiento de archivos con hash. n8n solo en los bordes (casilla, WhatsApp, SFTP, avisos). LLMs en tres lugares y siempre detrás de un umbral de confianza: extracción de facturas y PDFs, normalización de descripciones, explicación de excepciones y borrador de reglas. El costo de extracción son centavos por página; lo que cuesta son los minutos del operador.

### Riesgos
1. El acceso a bancos es por archivos por años: ser dueños de los parsers y hacer la carga sin fricción (mail, arrastrar, WhatsApp).
2. Los formatos de exporte cambian sin aviso: huella del layout, cuarentena y alerta, control de saldos como alarma.
3. Xubio puede hacer la versión barata adentro de sus paredes y Visma tiene distribución: ser agnósticos del ERP, operados y medidos; vender a pymes en Tango, Colppy y Contabilium y a contadores con carteras mixtas.
4. Extracción con LLM al 90% por campo: ninguna promesa de "sin tocar" sin umbral; publicar el número medido y la política de revisión.
5. Si la automatización se estanca abajo del 80%, las horas del operador se comen el fee: el ciclo de aprendizaje de reglas es el margen, no un lujo.
6. Credibilidad: nuestra diferenciación solo vale si los números se miden sobre los datos del cliente y se muestran.

## Comparación corta
| | Tramo | Asentia | Xubio | LFT | CargaFacturas |
|---|---|---|---|---|---|
| Tipo | servicio operado, agnóstico del ERP | SaaS archivo entra, archivo sale | función del ERP | agentes a medida | SaaS de cuentas a pagar |
| Bancos | PDF y exportes con parsers propios, mail y WhatsApp | PDF y archivos | PDF 3/mes, Excel 50 KB, Interbanking pago | archivos, "46 bancos" | no |
| Mercado Pago | API, cruce con el depósito | no | fuera de la conciliación | no lo menciona | no |
| Aprendizaje | decisiones a reglas en sombra | sí | no | ajustes del proveedor | no |
| Aprobación | tres bandejas, umbrales, firma por período | revisión de no conciliados | el usuario confirma | tesorería aprueba ajustes | cadenas por monto |
| Cuentas a pagar | sí, CAE y padrón contra ARCA | no | créditos de "Comprobantes IA" | sí | sí |
| Número publicado | medido por cliente | 92% sin método | ninguno | ilustrativo | 96% sin método |

Fuentes principales: asentia.io/blog/conciliacion-bancaria-automatica · xubio.com/ar/conciliacion-bancaria y ayuda.xubio.com · colppy.com/precios e intercom.help/Colppy · ayudas.axoft.com (Tango Banking) · contabilium.com/ar/planes · cargafacturas.com · theeye.io/reconciliations · lookingfortrouble.ai/agentes · duotach.com/blog/automatizar-conciliacion-bancaria · mercadopago.com.ar/developers (reportes) · interbanking.com.ar/planes · fiskil.com/open-finance-tracker/argentina · xubio.com/API/1.1/swagger.json · developer.alegra.com · docs.moderntreasury.com (reglas) · idp-leaderboard.org.
Sin verificar: menús de exporte de Galicia, Santander, BBVA, Macro, Nación y Bancor (sitios bloqueados); lista de bancos y precio de Prometeo; documentación de Interbanking; API de Colppy.
