/** Todo el copy de la landing vive acá. Los componentes solo lo muestran. Ningún número es real: todo lo marcado como ejemplo es ejemplo. */
export const site = {
  name: "Lionware",            // <- el nombre se cambia acá y en ningún otro lado
  tagline: "Automatizaciones con IA, bajo tu control.",
  email: "hola@lionware.com.ar", // placeholder: confirmar el mail antes de publicar
  nav: [
    { label: "Casos", href: "#casos" },
    { label: "Control", href: "#control" },
    { label: "Resultados", href: "#resultados" },
    { label: "Cómo trabajamos", href: "#proceso" },
  ],
  hero: {
    eyebrow: "Automatizaciones con IA · Finanzas y operaciones",
    lineOne: "Procesos que hoy lleva una persona,",
    lineTwo: "mañana corren solos. Con tus reglas.",
    lede: "Diseñamos, ponemos en producción y operamos automatizaciones que resuelven procesos completos dentro de tus sistemas. Permisos acotados, registro de cada acción, aprobación humana donde vos la definas, y un panel que muestra qué hizo y cuánto ahorró.",
    primary: { label: "Agendá una reunión", href: "#contacto" },
    secondary: { label: "Ver cómo trabajamos", href: "#proceso" },
    panel: {
      title: "cuentas-a-pagar · hoy",
      stats: [
        { label: "Facturas procesadas", value: "412" },
        { label: "Aprobaciones pedidas", value: "3" },
        { label: "Horas devueltas", value: "19,5" },
      ],
      log: [
        { time: "09:12:04", text: "Leyó FA 0001-00004410 del mail de proveedores", tag: "ok" },
        { time: "09:12:05", text: "Validó contra OC 7731 y remito 2210", tag: "ok" },
        { time: "09:12:07", text: "$ 7,4 M supera el umbral de $ 5 M", tag: "pendiente" },
        { time: "09:31:40", text: "Aprobó la gerencia de Cuentas a Pagar", tag: "aprobada" },
        { time: "09:31:41", text: "Registró la factura en el ERP", tag: "ok" },
      ],
      note: "Vista ilustrativa con datos de ejemplo",
    },
  },
  areas: ["Finanzas", "Compras", "Tesorería", "Operaciones", "Administración"],
  cases: {
    eyebrow: "De la necesidad a la automatización",
    title: "Nos contás el proceso. Lo diseñamos con tu equipo y lo operamos.",
    body: "Cada automatización se define con vos: qué hace, con qué permisos entra a cada sistema, qué controles aplica y dónde pide la firma de una persona.",
    columns: ["La necesidad", "La ficha", "En producción"],
    items: [
      {
        name: "Cuentas a pagar",
        need: { from: "Gerencia de Administración", via: "por mail · 09:02", text: "Procesamos más de 2.000 facturas por mes entre dos personas y el cierre se atrasa. ¿Se puede automatizar sin cambiar el sistema?" },
        reply: "Se puede, sin migrar nada. Diseñamos la automatización con permisos acotados y aprobación humana en cada umbral que definas.",
        sheet: [
          ["Lee", "mail de proveedores, PDF y XML"],
          ["Valida", "orden de compra y remito"],
          ["Registra", "en tu ERP, con permiso de escritura solo ahí"],
          ["Pide firma", "montos sobre el umbral que fijes"],
        ],
        live: [
          { time: "09:12", text: "FA 0001-00004410 validada", tag: "ok" },
          { time: "09:12", text: "Supera el umbral, requiere aprobación", tag: "pendiente" },
          { time: "09:31", text: "Aprobada y registrada", tag: "aprobada" },
        ],
      },
      {
        name: "Conciliación bancaria",
        need: { from: "Tesorería", via: "por WhatsApp · 17:40", text: "Tenemos seis cuentas en tres bancos y la conciliación nos lleva dos días por semana." },
        reply: "La automatización cruza extractos con el sistema contable y propone los ajustes. Tesorería los aprueba, no los busca.",
        sheet: [
          ["Lee", "extractos de cada banco"],
          ["Cruza", "contra el libro mayor"],
          ["Propone", "asientos de ajuste, uno por diferencia"],
          ["Pide firma", "cada ajuste antes de asentarlo"],
        ],
        live: [
          { time: "08:05", text: "6 cuentas leídas, 1.184 movimientos", tag: "ok" },
          { time: "08:09", text: "1.171 conciliados, 13 diferencias", tag: "ok" },
          { time: "08:09", text: "13 ajustes propuestos", tag: "pendiente" },
        ],
      },
      {
        name: "Reportes diarios",
        need: { from: "Dirección", via: "en reunión", text: "Cada lunes alguien arma a mano el reporte de ventas, cobranzas y stock. Llega tarde y con errores." },
        reply: "El reporte se arma solo cada mañana con el dato de cada sistema y de dónde sale. Nadie lo toca a mano.",
        sheet: [
          ["Lee", "ventas, cobranzas y stock de tus sistemas"],
          ["Arma", "el reporte con la fuente de cada cifra"],
          ["Entrega", "por mail o Slack antes de las 8"],
          ["Avisa", "si un dato falta o cambió más de lo normal"],
        ],
        live: [
          { time: "07:30", text: "3 sistemas leídos", tag: "ok" },
          { time: "07:31", text: "Stock de 2 depósitos fuera de rango", tag: "pendiente" },
          { time: "07:32", text: "Reporte enviado a 4 personas", tag: "ok" },
        ],
      },
    ],
  },
  control: {
    eyebrow: "Control",
    title: "Cada automatización opera bajo tus reglas.",
    body: "Una automatización en un proceso crítico necesita los mismos controles que cualquier sistema de tu empresa. Por eso nace con permisos, registro y aprobaciones definidos con tu gente.",
    items: [
      { name: "Permisos acotados", body: "Opera solo con los permisos que definís en cada sistema. Nada fuera de su alcance." },
      { name: "Registro de cada acción", body: "Qué leyó, qué decidió, con qué regla y quién aprobó. Nada queda sin rastro." },
      { name: "Aprobación humana", body: "Vos definís los umbrales: qué resuelve sola y qué necesita la firma de una persona." },
      { name: "Tus sistemas, tus datos", body: "Corre sobre lo que ya usás, en tu nube o en tu servidor. Los datos no salen de tu empresa." },
    ],
    panel: {
      title: "cuentas-a-pagar · permisos",
      permissions: [
        { system: "Mail de proveedores", level: "Lectura", on: true },
        { system: "ERP · facturas", level: "Registro", on: true },
        { system: "ERP · pagos", level: "Sin acceso", on: false },
        { system: "Banca electrónica", level: "Sin acceso", on: false },
      ],
      note: "Vista ilustrativa con datos de ejemplo",
    },
  },
  results: {
    eyebrow: "Resultados",
    title: "Cada número, medido. Con su fuente.",
    body: "Con cada automatización va un panel que muestra qué hizo, cuánto tiempo devolvió y qué falló, y de dónde sale cada cifra. Si no se puede medir, no lo prometemos.",
    panel: {
      title: "Panel del cliente · últimos 30 días",
      stats: [
        { label: "Corridas", value: "1.284", sub: "0 con error sin resolver" },
        { label: "Horas devueltas", value: "212", sub: "medidas contra el proceso anterior" },
        { label: "Aprobaciones humanas", value: "37", sub: "tiempo medio de firma 22 min" },
        { label: "Resueltas sin intervención", value: "91 %", sub: "el resto pasó por una persona" },
      ],
      provenance: [
        { metric: "Corridas", how: "cada ejecución registrada en el log de la automatización" },
        { metric: "Horas devueltas", how: "corridas × minutos del proceso manual medidos en la auditoría" },
        { metric: "Resueltas sin intervención", how: "corridas sin estado pendiente ÷ corridas totales" },
      ],
      note: "Ejemplo ilustrativo hasta tener datos de clientes",
    },
  },
  process: {
    eyebrow: "Cómo trabajamos",
    title: "Del primer llamado a producción en semanas, no meses.",
    steps: [
      { name: "Auditoría", body: "Treinta minutos sin costo. Salís con un mapa de qué automatizar primero y cuánto tiempo devuelve." },
      { name: "Diseño", body: "Definimos el proceso, los permisos y dónde entra una persona a aprobar." },
      { name: "Producción", body: "La primera automatización corriendo en tus sistemas en menos de treinta días." },
      { name: "Operación", body: "La operamos, la monitoreamos y la mejoramos. Cada mes ves qué hizo y qué ahorró." },
    ],
    cta: "Agendar auditoría",
  },
  faq: {
    eyebrow: "Preguntas",
    title: "Lo que suelen preguntar.",
    items: [
      { q: "¿Necesito cambiar de sistema?", a: "No. Trabajamos sobre el que ya tenés: ERP, banco, mail, planillas." },
      { q: "¿Qué pasa si la automatización se equivoca?", a: "Las acciones sensibles pasan por una persona antes de ejecutarse, y todo queda registrado para corregirlo." },
      { q: "¿Cómo se cobra?", a: "Un fee mensual por automatización que incluye operación, monitoreo y mejoras. Presupuesto cerrado antes de empezar." },
      { q: "¿Cuánto tarda?", a: "La primera automatización en producción en menos de treinta días desde la auditoría." },
      { q: "¿Dónde corre?", a: "En tu nube o en tu servidor. Los datos no salen de tu empresa." },
    ],
  },
  cta: {
    title: "¿Qué proceso llevamos a producción primero?",
    body: "Contanos en dos líneas y te respondemos el mismo día con una propuesta de reunión de treinta minutos. Sin compromiso.",
  },
  contact: {
    eyebrow: "Contacto",
    fields: { name: "Nombre y apellido", email: "Email laboral", company: "Empresa", message: "¿Qué proceso te gustaría automatizar?" },
    submit: "Enviar solicitud",
  },
};
export type Tag = "ok" | "pendiente" | "aprobada";
