# Plan de acción (05/11/2026)

Resumen de todas las investigaciones de hoy en una sola página. Los detalles están en cada documento: producto (`product/PRODUCTO.md`), mercado (`growth/MERCADO.md`), competencia (`growth/competencia.md`), plan comercial (`growth/PLAN-90-DIAS.md`), marco legal (`ops/LEGAL.md`), modelo de negocio (`ops/MODELO.md`), agentes (`eng/AGENTES.md`), ARCA (`eng/arca/README.md`).

## Lo que vendemos
**"Tu caja cerrada y controlada cada 15 días."** Bancos, Mercado Pago y tarjetas conciliados, facturas de proveedores registradas y verificadas contra ARCA, retenciones clasificadas por jurisdicción, diferencias explicadas, reporte de caja y semáforo de vencimientos, con fecha fija de entrega. Operado por nosotros, aprobado por una persona del cliente, medido en un panel. Lo jurídico financiero (altas, reducción de alícuotas, certificados) como adicional por evento.

## A quién
Pymes de 10 a 50 empleados del sur de Córdoba con 2 a 4 cuentas, tarjetas y Mercado Pago, sin tesorero: molinos, frigoríficos, carnicerías, distribuidoras, agro. Unas 7.000 en Córdoba. Y los estudios contables de Río Cuarto y Villa María como revendedores en marca blanca.

## Por qué nosotros
Nadie en el segmento pyme mide ni publica su tasa de automatización; nadie vende operado y presencial en el interior; nadie tiene casos de molino y carnicería; Xubio solo concilia adentro de Xubio; Asentia no tiene bancos ni precios; LFT no muestra un número real.

## Precio (a validar con 10 entrevistas)
Base $150.000, Operado $300.000, Tesorería $550.000 a 650.000 por mes más IVA, facturado en pesos al BNA. Marca blanca para estudios: $40.000 a 60.000 por cliente. Build inicial por proceso aparte.

## Las cuatro vías en paralelo

### 1. Comercial (Martín y Matías, desde esta semana)
- Semana 1: permiso de los dos clientes para el caso; postular a HackIA fase 2 como proveedor; lista de 40 contadores; pedir charla a CECIS y FEDECOM; comprar dominio.
- Semanas 3 a 5: 40 contadores en dos olas, 10 visitas en frío, diagnósticos de 2 horas, 2 pilotos firmados antes del cierre de año.
- Las 10 entrevistas de precio van dentro de los diagnósticos.
- Meta a 90 días: 6 a 8 pilotos pagos, 4 a 5 en fee mensual, 10 diagnósticos agendados para febrero.

### 2. Producto (el team de agentes más Matías, 12 semanas)
- Semanas 1 y 2: modelo de datos, parsers de los 7 bancos con exportes reales del molino y la carnicería, PDF con confianza por campo, control de saldos, deduplicación.
- Semanas 3 y 4: motor de matching en pasadas ordenadas, regla guardada en cada match, cola con motivos, suite de prueba (los seis casos de Duotach más 200 movimientos etiquetados por pyme). Primer número medido.
- Semanas 5 y 6: combinaciones, plantillas de neto de comisión (Mercado Pago, Getnet, Payway, SIRCREB, IIBB), Mercado Pago por OAuth de punta a punta.
- Semanas 7 y 8: consola del operador con tres bandejas, aprobación del cliente por web y WhatsApp, LLM para normalizar descripciones y explicar excepciones, decisiones a reglas en sombra.
- Semanas 9 y 10: escritura en Xubio por API, plantillas para Tango, Colppy y Contabilium; cuentas a pagar de la casilla al ERP con QR, constatación y padrón de ARCA.
- Semanas 11 y 12: panel del cliente, paquete de cierre, audit log, roles, política de retención escrita. Tres pymes en piloto pago con su número publicado.
- Stack: núcleo propio en TypeScript (o Python), Postgres, Temporal o Trigger.dev como motor de flujos, Next.js para consola y aprobaciones, Claude por API detrás de umbrales de confianza, n8n solo en los bordes. Cliente SOAP propio para ARCA, nunca AfipSDK.

### 3. Legal y administrativo (antes del primer cliente pago)
Certificado digital de Tramo y una emisión real por web service; procedimiento de delegación escrito; campo de condición de IVA listo antes del 01/12/2026; contrato con aprobación humana, responsabilidad fiscal en el cliente, incumbencias del contador y límite de responsabilidad; anexo de encargado de datos con cláusulas para Anthropic y OpenAI; bases inscriptas en el RNBD; política de seguridad y de credenciales (nunca claves fiscales ni de home banking); broker por seguro. Cada socio monotributista por ahora; SAS con 4 o 5 fees mensuales.

### 4. La empresa de agentes
- Local: `./team.sh` con lead, producto, backend, frontend, datos, ventas y qa sobre este repo.
- GitHub: la Action oficial de Claude Code para que un issue etiquetado se vuelva PR y cada PR tenga review; deploy automático de la landing ya armado.
- Rutinas en la nube de Claude Code para grooming nocturno, contenido semanal y reporte a los fundadores.
- Base de conocimiento: este repo. Los agentes leen `CLAUDE.md`, `TEAM.md` y los documentos de cada área antes de tocar nada.

## Decisiones que faltan de Martín y Matías
1. Nombre y dominio (Tramo colisiona con una fintech; tramo.com está en venta).
2. Qué se puede nombrar del molino y la carnicería.
3. Stack del núcleo: TypeScript o Python, según con qué esté más cómodo Matías.
4. Quién opera los cierres semanales los primeros tres meses (es el trabajo que define el margen).

## Riesgos que no se negocian
Bancos por archivo por años; formatos que cambian sin aviso; Xubio con distribución de Visma; extracción al 90% por campo; margen comido por el operador si la automatización no pasa del 80%; credibilidad solo con números medidos.
