/** Todo el copy de la landing vive acá. Los componentes solo lo muestran. */
export const site = {
  name: "startup",            // <- el nombre se cambia acá y en ningún otro lado
  tagline: "Automatizaciones con IA, bajo tu control.",
  email: "hola@example.com",  // placeholder
  hero: {
    title: "Automatizaciones con IA a medida, bajo tu control.",
    body: "Diseñamos, ponemos en producción y operamos automatizaciones que resuelven procesos completos dentro de tus sistemas, con permisos acotados, registro de cada acción y aprobación humana donde vos la definas.",
    primary: { label: "Agendá una reunión", href: "#contacto" },
    secondary: { label: "Ver cómo trabajamos", href: "#proceso" },
  },
  areas: ["Finanzas", "Compras", "Operaciones", "Administración"],
  services: {
    eyebrow: "Qué hacemos",
    title: "Procesos que hoy lleva una persona, mañana corren solos.",
    items: [
      { name: "Cuentas a pagar", body: "Lectura de facturas, control contra órdenes de compra y registro en tu sistema. Las que superan un monto, las aprueba alguien." },
      { name: "Conciliación bancaria", body: "Movimientos de banco contra el sistema contable, con los ajustes propuestos para que tesorería los apruebe." },
      { name: "Reportes y seguimiento", body: "Informes que hoy se arman a mano, listos cada mañana con el dato y de dónde sale." },
      { name: "Atención y soporte", body: "Respuestas a clientes y proveedores con la información de tus sistemas, y derivación a una persona cuando hace falta." },
    ],
  },
  process: {
    eyebrow: "Cómo trabajamos",
    title: "Del primer llamado a producción en semanas, no meses.",
    steps: [
      { name: "Auditoría", body: "Treinta minutos sin costo. Salís con un mapa de qué automatizar primero y cuánto tiempo devuelve." },
      { name: "Diseño", body: "Definimos el proceso, los permisos y dónde entra una persona a aprobar." },
      { name: "Producción", body: "La primera automatización corriendo en tus sistemas en menos de treinta días." },
      { name: "Operación", body: "La operamos, la monitoreamos y la mejoramos. Vos ves cada mes qué hizo y qué ahorró." },
    ],
  },
  control: {
    eyebrow: "Control",
    title: "Vos decidís qué puede hacer y qué no.",
    items: [
      { name: "Permisos acotados", body: "Cada automatización accede solo a lo que necesita." },
      { name: "Registro de cada acción", body: "Qué hizo, cuándo y con qué dato. Nada queda sin rastro." },
      { name: "Aprobación humana", body: "Umbrales que definís vos: por monto, por cliente, por tipo de operación." },
      { name: "Tus sistemas, tus datos", body: "Corre sobre lo que ya usás. Los datos no salen de tu empresa." },
    ],
  },
  results: {
    eyebrow: "Resultados",
    title: "Cada número, medido.",
    body: "Con cada automatización va un panel que muestra corridas, horas devueltas y errores, con la fuente de cada cifra. Si no se puede medir, no lo prometemos.",
    note: "Ejemplo ilustrativo hasta tener datos de clientes.",
  },
  faq: [
    { q: "¿Necesito cambiar de sistema?", a: "No. Trabajamos sobre el que ya tenés." },
    { q: "¿Qué pasa si la automatización se equivoca?", a: "Las acciones sensibles pasan por una persona antes de ejecutarse, y todo queda registrado para corregirlo." },
    { q: "¿Cómo se cobra?", a: "Un fee mensual por automatización que incluye operación, monitoreo y mejoras. Sin sorpresas." },
    { q: "¿Cuánto tarda?", a: "La primera automatización en producción en menos de treinta días desde la auditoría." },
  ],
  contact: {
    eyebrow: "Contacto",
    title: "¿Qué proceso llevamos a producción primero?",
    body: "Contanos en dos líneas y te respondemos el mismo día con una propuesta de reunión de treinta minutos.",
    fields: { name: "Nombre y apellido", email: "Email laboral", company: "Empresa", message: "¿Qué proceso te gustaría automatizar?" },
    submit: "Enviar",
  },
};
