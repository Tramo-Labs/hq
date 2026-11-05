# Cómo funciona el negocio (borrador del 05/11/2026)

Resumen de lo relevado en el mercado, adaptado a dos socios en el sur de Córdoba con dos pymes ya automatizadas. Rangos en USD; todo precio de referencia es de lista de otros proveedores, no hay encuestas independientes.

## 1. Modelo
Híbrido: **build fijo por proceso + fee mensual por pyme**. Es el default del mercado para pymes en 2026 y el que mejor combina caja al inicio con ingreso recurrente.
- Build: USD 800 a 2.000 por proceso (conciliación, cuentas a pagar, reporte). Los boutiques argentinos cobran 1.500 a 8.000; nosotros arrancamos abajo porque el objetivo del año es casos y referidos, no margen.
- Fee mensual: USD 300 a 800 por pyme. Incluye monitoreo, cambios cuando un banco o un sistema cambia, mejoras chicas y el reporte mensual. Los retainers locales van de 300 a 1.000 (Duotach: 800 a 3.000 para empresas más grandes).
- Herramientas (API, n8n, hosting) en cuentas del cliente o facturadas aparte a costo. No se absorben.
- Nada de precio por resultado: menos del 1% lo usa y más de la mitad de esos contratos se renegocia el primer año por discusiones sobre cómo medir.

## 2. Cómo se vende
1. **Diagnóstico de 2 horas en la empresa**, barato (USD 150 a 300) y acreditable al primer proceso. Gratis solo si el que lo trae es un referido. Se cronometra: las tres tareas repetitivas más caras, cuánto tiempo llevan, cuánto cuesta esa hora, cuántas veces por mes.
2. **Propuesta de una página**: problema, qué va a correr solo, qué aprueba una persona, métrica antes y después, precio.
3. **Primer proceso en 30 días** con métrica explícita (por ejemplo: la conciliación de los 6 bancos pasa de 2 días por semana a 2 horas). Si no se cumple, se devuelve el build.
4. **Revisión a los 90 días** para el segundo proceso. El 90% de los clientes de estas agencias se queda en retainer después del primer proyecto.
5. **Canales**: el molino y la carnicería como referidores; el contador de cada pyme como canal número dos (comisión del 10% sobre los primeros tres meses es lo habitual); HackIA 2026 del gobierno de Córdoba (Río Cuarto, Villa María, San Francisco, becas del 50% para pymes) como lugar donde están los compradores.

## 3. Cómo se entrega
- Stack: n8n (self-hosted; verificar la licencia antes de hostear una instancia para varios clientes), Claude y OpenAI por API con términos comerciales (no entrenan con los datos, retención de 30 días), código propio donde haga falta, ARCA, bancos y WhatsApp.
- Por cliente: diagrama del flujo en lenguaje llano, runbook, botón de apagado, accesos y logs que el cliente conserva aunque pause el servicio.
- Antes del primer fee: alerta de ejecución fallida y "latido" de cada automatización. Una agencia perdió un cliente de 2.500/mes porque un flujo estuvo caído tres días sin que nadie lo viera.
- Cada mes: reporte de una página. Corridas, errores, horas devueltas contra la línea base medida en el diagnóstico, aprobaciones humanas. Es el panel de resultados de la landing, en papel al principio.
- SLA razonable para pyme: respuesta en 4 horas hábiles, arreglos menores en 2 días hábiles, excluidas las caídas del banco o del ERP del cliente.

## 4. Contrato
Mensual, cancelable con 30 días. Datos, cuentas y código del cliente. No usamos sus datos para entrenar nada. Al irse, se entrega todo y se borra lo nuestro. Sin scope escrito no se arranca.

## 5. Modos de falla del primer año (y el antídoto)
- Sin nicho, compitiendo por precio → molinos, frigoríficos, carnicerías, distribuidoras. Nada más hasta tener seis.
- Fee que no paga el mantenimiento → el mantenimiento va en el precio desde el día uno.
- Rotura silenciosa → alertas antes del primer cobro.
- Churn en los primeros 90 días por falta de valor visible → un resultado visible en la primera semana y el reporte mensual sin falta.
- Vender chatbots vistosos → solo procesos con dolor cronometrado.
- Contratar gente antes de tener el modelo probado → nadie hasta 6 a 8 pymes en retainer.

## 6. Legal y administrativo (para mirar con un contador, no es asesoramiento)
- Arranque: cada socio como monotributista facturando por su cuenta mientras el total sea chico. El monotributo no admite socios ni separa patrimonio, y el tope de servicios (categoría H) ronda los USD 53.000 anuales desde agosto 2026.
- Pasar a **SAS** cuando haya 4 o 5 fees mensuales o un cliente del exterior. La RG 11/2026 de IGJ (vigente desde el 23/09/2026) simplificó la constitución; el costo real es el mantenimiento mensual (contador, IVA, Ganancias, IIBB, autónomos).
- Facturar afuera: alta de exportador de servicios y Factura E; desde septiembre 2025 una persona humana puede conservar los dólares sin tope. Para una SAS no está confirmado que aplique lo mismo.
- Economía del Conocimiento: solo tiene sentido con sociedad y exportación real.

## 7. Meta del año uno
De 6 a 8 pymes en fee mensual, entre USD 3.000 y 5.000 recurrentes por mes, antes de contratar a nadie o abrir otro rubro. Las dos pymes actuales convertidas en casos con números medidos antes de fin de año.
