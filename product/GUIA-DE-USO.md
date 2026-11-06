# Guía de uso de Tramo (borrador del 06/11/2026)

Para quien usa el producto, sin lenguaje técnico. Dos lectores: la empresa cliente y quien opera (Tramo o un estudio contable). Lo que todavía no existe está marcado como **próximamente**. Todo número de esta guía es ejemplo ilustrativo.

## Qué es Tramo, en dos frases

Tramo te cierra la caja cada quince días: toma los extractos de tus bancos y de Mercado Pago, tus facturas y lo que tu sistema contable tiene sin conciliar, une cada movimiento con su comprobante y te deja un resumen con cada número y de dónde sale. Vos solo decidís lo que la máquina no puede decidir sola, y lo decidís desde el celular en cinco minutos.

Tramo no mueve plata, no entra a tu home banking y no te pide la clave fiscal. Nunca.

## Parte 1. Para la empresa cliente

### 1.1 Cómo entrás

Recibís un mail de invitación. El link entra solo; si querés, definís una contraseña. Si perdés el mail, en la página de ingreso pedís "Entrar con un enlace por mail" y te llega uno nuevo. Los links se usan una sola vez y vencen.

Si ya tenés cuenta en Tramo por otra empresa, el mismo link te suma a la nueva y elegís con cuál trabajar desde el menú de arriba.

### 1.2 Los tres pasos para arrancar

Cuando entrás por primera vez ves una lista de tres pasos. Cada uno queda en pendiente, verificando, listo o con problema, y "con problema" siempre dice qué hacer.

1. **ARCA.** Confirmás el CUIT de la empresa. Tramo verifica que existe y guarda razón social y condición frente al IVA. Nada más. Si más adelante contratás emisión de facturas, en este mismo paso vas a delegar ese servicio a Tramo desde tu cuenta de ARCA, con capturas paso a paso (**próximamente**).
2. **Bancos.** Das de alta cada cuenta (banco, tipo, moneda, últimos cuatro del CBU) y subís el primer extracto como lo exporta tu home banking (CSV, Excel o PDF). Lo arrastrás al portal. Mercado Pago es el único con botón "Conectar". El paso queda listo cuando un extracto se lee bien y el saldo cierra. Si algo no cierra, te decimos exactamente qué rango falta.
3. **Sistema contable.** Elegís tu sistema. Si tiene API (Xubio, Odoo, Alegra), generás una credencial en tu sistema siguiendo las capturas y la pegás; se guarda cifrada y la podés revocar cuando quieras. Si no tiene (Tango, Colppy, Contabilium, planilla), subís el exporte de partidas abiertas o del mayor de bancos cada período, igual que el extracto.

Con los tres listos, el portal pasa a la vista normal.

### 1.3 Lo único que tenés que decidir: la bandeja de excepciones

Cada quince días (o cuando subís un extracto) Tramo concilia. Lo que se resuelve con una regla exacta se registra solo. Lo que tiene alguna duda lo revisa el operador. Y lo que solo vos podés saber te llega a la bandeja de excepciones. Por ejemplo: una transferencia de $ 1.250.000 del 4/11 sin ninguna factura que la explique.

Cada ítem muestra qué es en una frase, la evidencia (el movimiento, la factura, con link al original), por qué no se resolvió solo y una propuesta si la hay. Tres botones:

- **Aprobar**: se registra la propuesta.
- **Rechazar**: no se registra, vuelve al operador con tu motivo (el motivo es obligatorio).
- **Es otra cosa**: escribís qué es y vuelve al operador, que lo vincula a mano.

Una decisión queda registrada con quién, cuándo y desde dónde. No se edita: si te equivocaste, la revertís y eso también queda registrado. Cuando entran excepciones nuevas te llega un mail, como mucho uno por día, más el resumen del lunes.

### 1.4 El panel de resultados

Cada número tiene un link a la lista de lo que lo compone; si no se puede abrir, no está. Ves por cuenta y por mes: tasa de conciliación automática, excepciones abiertas y su antigüedad, diferencia entre saldo bancario y contable hoy, facturas procesadas, retenidas y duplicados atrapados. "Sin datos todavía" y "cero" se muestran distinto.

### 1.5 Quién entra a tu empresa

Desde Miembros, el dueño invita gente con un rol:

| Rol | Qué puede |
|---|---|
| Dueño (owner) | Todo: decide excepciones, invita y quita gente, maneja credenciales, descarga todo |
| Aprobador (approver) | Decide excepciones y sube archivos |
| Solo lectura (viewer) | Ve todo, no toca nada |
| Contador (accountant) | Ve y descarga; puede tener varias empresas a la vez; no aprueba |

Sumar a alguien es siempre una invitación que esa persona acepta. Hasta que acepte, no tiene acceso. Al último dueño no se lo puede quitar.

### 1.6 Tus archivos y tu salida

En Archivos ves cada extracto y documento subido, con fecha, quién lo subió y estado. "Descargar todo" arma un ZIP con los originales y, en CSV, movimientos, documentos, conciliaciones con su regla, decisiones y el registro de auditoría. Es tuyo y te lo llevás cuando quieras.

### 1.7 Lo que nunca te vamos a pedir

La clave fiscal, la clave del home banking y permiso para pagar. No hay ningún campo donde quepan.

## Parte 2. Para el operador (Tramo o un estudio contable)

### 2.1 Alta de un cliente

Creás la empresa con razón social y CUIT e invitás al dueño por mail. En Clientes lo ves como "esperando al cliente" hasta que complete los tres pasos; la lista muestra qué le falta a cada uno y hace cuánto, con lo más trabado arriba.

### 2.2 Las tres bandejas

| Bandeja | Qué entra | Quién decide |
|---|---|---|
| Automático | regla exacta sin ambigüedad: referencia única, monto exacto en ventana de fechas, regla del cliente confirmada | nadie; se registra con la regla y se puede revisar |
| Sugerido | tolerancia de monto, combinaciones, propuestas del modelo, cualquier ajuste | vos |
| Excepciones | sin candidato, posible duplicado, saldo que no cierra, proveedor no autorizado, monto sobre el umbral, factura que ARCA no constata | el cliente |

Dos candidatos igual de buenos nunca se resuelven solos: van a Sugerido. En Sugerido aprobás, rechazás o escalás a Excepciones. Lo que el cliente marcó "es otra cosa" te vuelve con su nota: con **Vincular a mano** elegís las partidas abiertas cuya suma coincide exacto con el movimiento.

### 2.3 Cuarentena

Un extracto con formato desconocido, o con un saldo que no cierra, no se lee a medias: cae en Cuarentena con el motivo. Lo revisás, y si es un formato nuevo se agrega un lector; el cliente ve "lo revisamos y te avisamos".

### 2.4 Cierre quincenal

Por cliente, el cierre congela el período y arma el resumen que le llega al cliente. No se cierra con Sugerido pendiente: primero se vacía la cola. **Próximamente**: el cierre mensual corre solo y te avisa.

### 2.5 El panel del operador

El mismo panel del cliente, más las reglas que más disparan y las que nunca disparan, por cliente. Un operador ve solo las empresas que opera.

## Parte 3. Preguntas frecuentes

**¿Tramo paga o mueve plata?** No. Ninguna acción de ninguna bandeja mueve plata.

**¿Qué pasa si subo dos veces el mismo extracto?** No se procesa dos veces; te avisa cuándo lo habías subido.

**¿Puedo deshacer una aprobación?** Sí, como una acción nueva que también queda registrada. Nada se borra.

**¿Mi contador puede entrar?** Sí, con rol contador: ve y descarga todo, no aprueba. Si tu estudio opera Tramo por vos, entra como operador.

**¿Dónde quedan mis datos?** En una base propia de tu empresa, cifrados, con copia diaria. Los comprobantes se guardan diez años como exige la ley. Si te vas, descargás todo.

## Qué falta para que esta guía sea real

Delegación de facturación electrónica en el paso ARCA, casilla de mail propia por cliente para mandar extractos, segundo factor, cierre mensual automático con aviso, y el núcleo publicado en un servidor (hoy el portal se muestra con datos de ejemplo).
