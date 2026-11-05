# Tablero del team

Objetivo del mes: tres pymes de diseño con sus extractos cargados, el motor de matching con un número medido por pyme, y la landing con los dos casos reales. El plan completo está en `ops/PLAN-DE-ACCION.md`.

| # | Tarea | Dueño | Estado | Nota |
|---|-------|-------|--------|------|
| 1 | Modelo de datos: cuenta, extracto, movimiento, ítem contable, match, regla, decisión, excepción | backend | pendiente | ver product/PRODUCTO.md |
| 2 | Parsers de extractos: Galicia, Santander, BBVA, Macro, Nación, Credicoop, Bancor, con exportes reales | datos | pendiente | control de saldos antes de todo; huella del layout |
| 3 | Lector de PDF de extractos y facturas con confianza por campo; QR de ARCA primero | datos | pendiente | eng/arca/README.md |
| 4 | Motor de matching: pasadas ordenadas, regla en cada match, cola con motivos | backend | pendiente | suite: seis casos de Duotach más 200 movimientos etiquetados |
| 5 | Cliente SOAP propio de ARCA: WSAA, constancia de inscripción, WSCDC | backend | pendiente | nunca AfipSDK; simulador arca-api-sim para CI |
| 6 | Mercado Pago por OAuth: reportes de liquidaciones y dinero en cuenta | backend | pendiente | |
| 7 | Consola del operador con tres bandejas y página de aprobación del cliente | frontend | pendiente | Next.js, mismo diseño que la landing |
| 8 | Panel de resultados por cliente con la fuente de cada número | frontend | pendiente | |
| 9 | Landing: casos reales, formulario que llegue a un mail, botón de WhatsApp | frontend | pendiente | esperar nombre y mail |
| 10 | Lista de 40 contadores y mensaje de apertura; one-pager para contadores | ventas | pendiente | drafts; nunca enviar |
| 11 | Postulación a HackIA fase 2 como proveedor: borrador | ventas | pendiente | uvitec.org.ar |
| 12 | QA: build, lint, suite del motor, vista en celular | qa | pendiente | |
