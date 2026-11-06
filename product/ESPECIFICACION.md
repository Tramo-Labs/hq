# Especificación por módulo (06/11/2026)

Qué tiene que hacer cada parte, vista desde quien la usa. Lo técnico (stack, tokens, cifrado, RLS) está en `eng/ARQUITECTURA.md` y no se repite acá. Roles: los cinco de la sección 5 de la arquitectura. Onboarding: los tres pasos de la sección 4. Cada sección cierra con criterios de aceptación: si no se pueden probar, están mal escritos.

Todo monto, fecha o cantidad de esta página es ejemplo ilustrativo. Ningún número de cliente es real todavía.

## 0. Vocabulario

- **Cliente**: la empresa (un tenant). **Operador**: Tramo, o el estudio contable que opera el software.
- **Movimiento**: una línea de extracto (banco o Mercado Pago). **Documento**: una factura, nota o comprobante. **Partida abierta**: lo que el ERP del cliente tiene sin conciliar.
- **Match**: la unión de uno o más movimientos con uno o más documentos o partidas. Siempre lleva la regla que lo hizo.
- **Bandejas**: Automático, Sugerido, Excepciones (sección 5).

## 1. Roles y qué ve cada uno

| Puede | owner | approver | viewer | accountant | operator |
|---|---|---|---|---|---|
| Ver panel, movimientos, documentos y matches de su tenant | sí | sí | sí | sí | sí |
| Completar el onboarding (CUIT, extractos, ERP) | sí | no | no | no | sí, salvo lo que exige la clave fiscal del cliente |
| Subir extractos y facturas | sí | sí | no | sí | sí |
| Decidir la bandeja Excepciones | sí | sí | no | no | no |
| Decidir la bandeja Sugerido | no | no | no | no | sí |
| Fijar umbrales y reglas del cliente | sí | no | no | no | propone, el owner confirma |
| Invitar y quitar usuarios del tenant | sí | no | no | no | solo al primer owner |
| Cargar o revocar credenciales de ERP y Mercado Pago | sí | no | no | no | no |
| Descargar todo lo del tenant | sí | no | no | sí | sí |
| Ver el audit log del tenant | sí | no | no | sí | sí |
| Ver varios tenants a la vez | no | no | no | sí, los que tenga asignados | sí, los que opera |

Reglas que salen de la tabla:
- El cliente nunca ve la bandeja Sugerido ni reglas internas sin confirmar. Ve el resultado: qué se registró y con qué regla.
- El contador del cliente (`accountant`) lee y descarga; no aprueba. Si un estudio opera clientes, sus personas tienen `operator` en esos tenants, no `accountant`.
- Segundo factor obligatorio para `owner`, `approver` y `operator`. Para `viewer` y `accountant`, opcional. (Decisión abierta 2.)
- Toda acción de esta tabla queda en el audit log con quién, cuándo y desde dónde.

## 2. Alta de un cliente

1. El operador crea el tenant con razón social y CUIT, e invita al owner por mail.
2. El owner entra por link mágico, define contraseña si quiere y activa el segundo factor. Sin segundo factor no pasa de esta pantalla.
3. Aterriza en el onboarding (sección 3). El operador ve al cliente en su consola como "esperando al cliente".

Aceptación: un owner invitado entra, activa TOTP y ve el onboarding; un link mágico usado o vencido no entra; el operador no puede crear un segundo owner: lo invita el primero.

## 3. Portal del cliente: onboarding de tres pasos

Una lista con tres pasos. Cada paso tiene uno de cuatro estados: **pendiente**, **verificando**, **listo**, **con problema**. "Con problema" siempre trae una frase que dice qué hacer, nunca un código de error.

### Paso 1. ARCA

Qué hace el cliente: confirma su CUIT. Para conciliación y cuentas a pagar no hace falta nada más.

Cómo se verifica: el gateway consulta la constancia de inscripción. Si responde, el paso queda listo y se guardan razón social, domicilio fiscal y condición frente al IVA.

Ojo: la constancia, la constatación de facturas y la consulta de apócrifos se hacen con el CUIT de Tramo y **no necesitan delegación del cliente** (`eng/arca/README.md`). Que la constancia responda prueba que el CUIT existe, no que el cliente delegó algo.

**Provisorio (06/11, lead):** en esta etapa el paso 1 se muestra como **"CUIT confirmado"** y nada más. El sub-paso de delegación de abajo no se construye hasta que Martín decida (decisión abierta 1).

La delegación solo hace falta si el cliente contrata emisión de facturas. La propuesta es que ahí, dentro del mismo paso 1, aparezca **"Delegar facturación electrónica"**:
- El cliente crea el punto de venta "RECE para aplicativo y web services" y delega el servicio al CUIT de Tramo, siguiendo capturas del portal (checklist de delegación en `eng/arca/README.md`).
- El operador acepta la designación en ARCA.
- Se verifica pidiendo el último comprobante autorizado con el CUIT del cliente. Si ARCA contesta 600 o 601, el estado es "con problema: falta completar la delegación en ARCA", con el paso que falta.
- Se guardan CUIT, punto de venta y fecha de la delegación.

Aceptación: un CUIT inexistente queda con problema y mensaje legible; un CUIT válido queda listo sin intervención; la delegación no se marca lista solo porque respondió la constancia.

### Paso 2. Banco

Qué hace el cliente: da de alta cada cuenta (banco, tipo, moneda, últimos cuatro del CBU) y sube el primer extracto. Tres formas:
- Arrastrar el archivo del home banking (CSV, XLS o PDF) al portal.
- Mandarlo por mail a su casilla propia, que el portal muestra con un botón de copiar.
- Mercado Pago: botón "Conectar" (OAuth). Es la única cuenta con conexión directa.

Cómo se verifica, por cuenta: el primer extracto se lee, el formato se reconoce y el saldo cierra (saldo inicial más créditos menos débitos igual a saldo final). Recién ahí la cuenta queda lista.

Estados con problema y su frase (fechas: ejemplo ilustrativo):
- "No reconocemos este formato. Lo revisamos y te avisamos." Va a cuarentena y le llega al operador.
- "El saldo no cierra: faltan movimientos entre el 3 y el 7 de octubre. ¿Podés exportar ese rango?"
- "Este archivo ya lo subiste el 2 de noviembre." No es error: no se procesa dos veces.

El paso queda listo cuando hay al menos una cuenta lista. El portal muestra cuántas cuentas hay y cuándo llegó el último extracto de cada una.

Nunca se pide usuario ni clave del home banking. No hay campo donde quepa.

Aceptación: un exporte de cada banco de la tarea 4 deja la cuenta lista; un archivo con un movimiento borrado queda con problema de saldo; el mismo archivo subido dos veces no duplica movimientos; un formato desconocido llega a la consola del operador.

### Paso 3. Sistema contable

Qué hace el cliente: elige su sistema de una lista.
- **Con API** (Xubio, Odoo, Alegra): genera la credencial en su sistema siguiendo capturas y la pega en el portal. Se guarda cifrada, se muestran solo los últimos caracteres y tiene botón "Revocar".
- **Sin API** (Tango, Colppy, Contabilium, planilla): sube el exporte del mayor de bancos o de las partidas abiertas. Se repite cada período, igual que el extracto.

Cómo se verifica: con API, una lectura de prueba responde y trae la lista de cuentas. Sin API, el primer exporte se lee bien.

Aceptación: una credencial inválida queda con problema y no se guarda; revocar borra la credencial y deja el paso pendiente; ninguna pantalla vuelve a mostrar la credencial completa.

### Con los tres pasos listos

El portal pasa a la vista normal: bandeja, panel, archivos. El onboarding queda en configuración para agregar cuentas o cambiar el ERP.

## 4. Portal del cliente: uso diario

### Bandeja de excepciones

Lo único que el cliente tiene que decidir. Cada ítem muestra:
- Qué es, en una frase. Ejemplo ilustrativo: "Transferencia de $ 1.250.000 del 4/11 sin factura que la explique".
- La evidencia: el movimiento, el documento o la partida, con link al original.
- Una explicación en castellano de por qué no se resolvió solo.
- La propuesta, si la hay.

Tres acciones: **Aprobar** (se registra la propuesta), **Rechazar** (no se registra y vuelve al operador con el motivo) y **Es otra cosa** (el cliente escribe qué es y vuelve al operador). Una decisión no se edita: revertirla es una acción nueva que también queda registrada.

Motivos que caen acá: sin candidato, posible duplicado, saldo que no cierra, factura de proveedor no autorizado o sin orden de compra, monto sobre el umbral del cliente, factura que ARCA no constata.

Avisos: un mail cuando entran excepciones nuevas (agrupado, como mucho uno por día) y el resumen del lunes.

Aceptación: aprobar registra quién, cuándo e IP, y aplica la propuesta una sola vez aunque el botón se toque dos veces; un viewer ve la bandeja sin botones; un ítem decidido no vuelve a aparecer.

### Panel de resultados

Cada número tiene un link a la lista de lo que lo compone. Si no se puede abrir, no va en el panel.
- Tasa de conciliación automática por cuenta y por mes.
- Excepciones abiertas y su antigüedad.
- Diferencia entre saldo bancario y saldo contable hoy, por cuenta.
- Facturas procesadas, retenidas y duplicados atrapados.
- Horas devueltas: solo si hay una línea base que el cliente confirmó al arrancar. Sin línea base, el número no aparece.

"Sin datos todavía" y "cero" son cosas distintas y se muestran distinto.

### Archivos y descarga

- Lista de extractos y documentos subidos, con fecha, quién lo subió y estado.
- **Descargar todo**: un ZIP con los originales y, en CSV, movimientos, documentos, matches con su regla, decisiones y audit log. Se arma en segundo plano, llega un link por mail que vence en 24 horas, y la descarga queda en el audit log.

### Configuración (solo owner)

Usuarios y roles, umbrales (monto a partir del cual una factura pide firma), credenciales conectadas con fecha y botón de revocar, delegaciones de ARCA vigentes.

## 5. Las tres bandejas

| Bandeja | Qué entra | Quién decide | Qué pasa |
|---|---|---|---|
| Automático | regla exacta sin ambigüedad: referencia única, monto exacto en ventana de fechas, regla del cliente confirmada | nadie | se registra con la regla y se puede revisar después |
| Sugerido | tolerancia de monto, combinaciones, propuestas del modelo, cualquier ajuste | operator | aprobar, rechazar o mandar a Excepciones |
| Excepciones | los motivos de la sección 4 | owner o approver | aprobar, rechazar, es otra cosa |

Dos candidatos igual de buenos nunca se resuelven solos: van a Sugerido. Nada en ninguna bandeja mueve plata.

## 6. Consola del operador

- **Clientes**: cada tenant con los pasos de onboarding que faltan, hace cuánto, y el último extracto por cuenta. Lo más trabado arriba.
- **Bandejas**: Sugerido de todos sus clientes en una cola, filtrable por cliente y por motivo. Excepciones en solo lectura, con antigüedad, para perseguir al cliente.
- **Cuarentena**: formatos de extracto no reconocidos y archivos con saldo que no cierra.
- **Cierre semanal**: por cliente, congela la semana y arma el resumen del lunes. No se cierra con Sugerido pendiente.
- **Panel**: el mismo del cliente, más las reglas que más disparan y las que nunca disparan.

Aceptación: un operator con dos tenants ve esos dos y ninguno más; un cierre con Sugerido pendiente se rechaza con el motivo.

## 7. Módulos del núcleo, a nivel producto

**Identidad y tenants.** Que cada persona entre a lo suyo y a nada más; un usuario puede estar en varios tenants y elige en cuál trabaja. Aceptación: un tenant no ve datos de otro aunque el código pida mal; reusar un refresh viejo cierra todas las sesiones de esa familia; a un usuario que sacan del tenant se le corta el acceso en menos de 10 minutos.

**Ledger.** Que cada movimiento esté una sola vez y con saldo controlado. Aceptación: los cinco formatos de la tarea 4 se leen con fixtures sintéticos; un saldo que no cierra frena el extracto; un formato que cambió se detecta por huella y va a cuarentena en vez de leerse mal.

**Documents.** Que cada factura tenga sus datos con la fuente y la confianza de cada uno. Orden: QR de ARCA, texto del PDF, modelo de lenguaje. Aceptación: una factura con QR completa CUIT, tipo, número, importe y CAE sin modelo; un campo con confianza baja no alimenta un match automático.

**Matching.** Unir movimientos con documentos o partidas y decir por qué. Aceptación: pasan los seis casos de Duotach y los 200 movimientos etiquetados; cada match tiene regla y versión; correr dos veces el mismo período no crea matches nuevos; dos candidatos iguales van a Sugerido.

**Aprobaciones.** Toda decisión humana con quién, cuándo y sobre qué. Aceptación: una decisión no se edita ni se borra; la misma decisión enviada dos veces se aplica una vez.

**Gateway de ARCA.** Hablar con ARCA sin que la clave salga de un proceso. Aceptación: contra el simulador responden constancia de un CUIT, constatación de una factura y último autorizado; un pedido de CAE reintentado nunca emite dos veces.

## 8. Fuera de alcance de esta etapa

WhatsApp como canal de carga o aprobación. Interbanking y APIs de bancos. Pantalla de reglas aprendidas en modo sombra. Escritura por API en Tango, Colppy y Contabilium. Pagos de cualquier tipo.

## 9. Decisiones abiertas

1. **Verificación del paso ARCA.** `eng/ARQUITECTURA.md` §4 lo da por verificado cuando responde la constancia, pero la constancia no prueba la delegación. Propuesta: lo de la sección 3. Decide: lead.
2. **Segundo factor.** La arquitectura lo exige "para quien aprueba pagos", pero nada mueve plata. Propuesta: obligatorio para owner, approver y operator. Decide: lead.
3. **De quién es el código.** `ops/MODELO.md` dice "datos, cuentas y código del cliente". Con el núcleo vendido como producto (D-10, D-11), el código no puede ser de cada cliente. Propuesta para el contrato: datos y configuración del cliente; software licenciado. Decide: Martín.
