/** Todo el copy de la landing vive acá. Los componentes solo lo muestran. Ningún número es real: todo lo marcado como ejemplo es ejemplo. */
export const site = {
  name: "Tramo",               // <- el nombre se cambia acá y en ningún otro lado
  tagline: "Automatizaciones con IA, bajo tu control.",
  email: "hola@lionware.com.ar", // placeholder: confirmar el mail antes de publicar
  nav: [
    { label: "El problema", href: "#problema" },
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
        slug: "cuentas-a-pagar",
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
        slug: "conciliacion",
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
        slug: "reportes",
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
  integrations: {
    label: "Trabaja con lo que ya usás",
    items: ["Tango", "Colppy", "Xubio", "Odoo", "SAP Business One", "Google Sheets", "Excel", "Gmail", "WhatsApp", "Mercado Pago", "Banco Galicia", "Banco Nación", "Santander", "Bancor", "Interbanking", "ARCA"],
  },
  howItWorks: {
    eyebrow: "Cómo se compra",
    title: "Una auditoría gratis, un proceso en producción, un fee por mes.",
    items: [
      { n: "01", name: "Auditoría sin costo.", body: "Treinta minutos con tu equipo. Salís con el mapa de qué automatizar primero y cuánto tiempo devuelve." },
      { n: "02", name: "Primer proceso en 30 días.", body: "Lo diseñamos con tus reglas y lo ponemos a correr sobre los sistemas que ya usás. Sin migraciones." },
      { n: "03", name: "Lo operamos nosotros.", body: "Un fee mensual por automatización que incluye operación, monitoreo y mejoras. Cancelás cuando quieras." },
    ],
  },
  realCases: {
    eyebrow: "En producción",
    title: "Ya corre en empresas del interior de Córdoba.",
    // CONFIRMAR con Martín y su socio antes de publicar: nombres, qué hace cada una y si se puede nombrar al cliente.
    items: [
      { sector: "Molino harinero · La Carlota", what: "Conciliación bancaria automatizada contra el sistema contable, con los ajustes aprobados por administración." },
      { sector: "Carnicería · La Carlota", what: "Automatizaciones de operación diaria: pedidos, stock y reportes, con el dato de cada sistema." },
    ],
    note: "Sin números hasta medirlos con el cliente.",
  },
  assistant: {
    label: "Contanos tu proceso",
    cta: "Contar",
    prompts: [
      "Contanos qué proceso te lleva más tiempo y te mostramos cómo automatizarlo.",
      "¿Cuántas cuentas bancarias conciliás por semana?",
      "¿Qué reporte armás a mano todos los lunes?",
      "¿Qué sistemas usás hoy? Tango, Colppy, Excel, WhatsApp...",
    ],
  },
  manifesto: {
    eyebrow: "Tramo / Lo que hacemos",
    title: "Que la administración corra sola.",
    body: "No vendemos inteligencia artificial. Vendemos que la conciliación esté hecha el lunes a las 8, que ninguna factura se pague dos veces y que el reporte llegue solo. La IA es cómo lo hacemos.",
    cta: { label: "Iniciar un proyecto", href: "#contacto" },
  },
  offerLine: "Diagnóstico de 2 horas sin costo. Un fee mensual por proceso. Cancelás cuando quieras. Todo queda sobre tu sistema.",
  whatsapp: "5493584000000", // placeholder: poner el número real con código de país, sin signos
  problem: {
    eyebrow: "El problema",
    title: "La plata se pierde en lo que nadie mira.",
    stats: [
      { value: "49 días", label: "tarda en promedio una pyme en cobrar", source: "Observatorio PyME, tercer trimestre 2026" },
      { value: "1 de 7", label: "pymes industriales enfrenta embargos de ARCA", source: "Observatorio PyME, octubre 2026" },
      { value: "$840.000", label: "al año retenidos de más en Ingresos Brutos en un caso real de Córdoba", source: "Asert, ejemplo publicado" },
      { value: "60 días", label: "y el extracto del banco se da por conforme si nadie lo revisó", source: "BCRA" },
    ],
    body: "No es que tu administración trabaje poco. Es que concilia a fin de mes, cuando el saldo a favor ya se congeló y el vencimiento ya pasó.",
  },
  paths: {
    eyebrow: "Por dónde empezar",
    title: "¿Sos la empresa o el estudio contable?",
    items: [
      { name: "Para empresas", body: "Tu administración al día cada 15 días: bancos, Mercado Pago y facturas de proveedores, con tu aprobación y un panel que te muestra qué pasó.", cta: "Agendar el diagnóstico", href: "#contacto" },
      { name: "Para estudios contables", body: "Recibís cada mes la conciliación hecha y clasificada de tus clientes, en el formato de tu sistema. Vos seguís firmando; nosotros hacemos el trabajo que menos te gusta.", cta: "Hablar por el estudio", href: "#contacto" },
    ],
  },
  never: {
    eyebrow: "Lo que no hacemos",
    title: "Tres cosas que nunca te vamos a pedir.",
    items: [
      { name: "Tu clave fiscal", body: "Trabajamos con los servicios que vos delegás a nuestro CUIT desde ARCA, y se revocan cuando quieras." },
      { name: "Tu clave del home banking", body: "Los movimientos salen del exporte que ya hacés o de un token a tu nombre. Nunca de tu contraseña." },
      { name: "Permiso para pagar", body: "Ningún pago sale sin que lo apruebe una persona tuya. Nosotros proponemos; vos decidís; tu contador firma." },
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
      { name: "Auditoría", when: "Día 0", body: "Treinta minutos sin costo. Salís con un mapa de qué automatizar primero y cuánto tiempo devuelve." },
      { name: "Diseño", when: "Semana 1", body: "Definimos el proceso, los permisos y dónde entra una persona a aprobar." },
      { name: "Producción", when: "Día 30", body: "La primera automatización corriendo en tus sistemas en menos de treinta días." },
      { name: "Operación", when: "Cada 15 días", body: "La operamos, la monitoreamos y la mejoramos. Cada mes ves qué hizo y qué ahorró." },
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
