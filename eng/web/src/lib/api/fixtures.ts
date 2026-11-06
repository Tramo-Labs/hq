import type { Match, Session, Tenant, UploadedFile } from "./types";

/**
 * Synthetic data for the portal and the console until the core serves these paths.
 * Every company, CUIT, amount and date here is invented; the UI labels it as sample data.
 */

/** Fixed "today" so sample ages do not drift between builds. */
export const asOf = "2026-11-06";

export const session: Session = { name: "Lucía Ferreyra", role: "owner" };
export const operatorSession: Session = { name: "Operador Tramo", role: "operator" };

export const tenants: Tenant[] = [
  {
    id: "t-agro", name: "Agroinsumos del Sur SA", cuit: "30-00000001-0", openExceptions: 3, waitingSince: "2026-11-03",
    arca: { status: "ready", confirmedAt: "2026-11-02" },
    bank: {
      status: "ready",
      accounts: [
        { bank: "Galicia", kind: "Cuenta corriente", currency: "ARS", last4: "4021", status: "ready", lastStatementAt: "2026-11-03" },
        { bank: "Macro", kind: "Caja de ahorro", currency: "USD", last4: "7730", status: "problem", problem: "El saldo no cierra: faltan movimientos entre el 3 y el 7 de octubre. ¿Podés exportar ese rango?" },
      ],
    },
    erp: { status: "pending" },
  },
  {
    id: "t-frio", name: "Frigorífico La Pampa SRL", cuit: "30-00000002-9", openExceptions: 0, waitingSince: "2026-10-27",
    arca: { status: "pending" },
    bank: { status: "pending", accounts: [] },
    erp: { status: "pending" },
  },
  {
    id: "t-ferr", name: "Ferretería Industrial Norte", cuit: "30-00000003-7", openExceptions: 1, waitingSince: "2026-10-31",
    arca: { status: "ready", confirmedAt: "2026-10-28" },
    bank: {
      status: "checking",
      accounts: [{ bank: "Bancor", kind: "Cuenta corriente", currency: "ARS", last4: "1188", status: "checking", lastStatementAt: "2026-11-05" }],
    },
    erp: { status: "ready", system: "Odoo", mode: "api", credentialTail: "…XXXX" },
  },
  {
    id: "t-trans", name: "Transportes Río Cuarto SA", cuit: "30-00000004-5", openExceptions: 0,
    arca: { status: "ready", confirmedAt: "2026-10-20" },
    bank: {
      status: "ready",
      accounts: [
        { bank: "Santander", kind: "Cuenta corriente", currency: "ARS", last4: "5502", status: "ready", lastStatementAt: "2026-11-04" },
        { bank: "Mercado Pago", kind: "Cuenta", currency: "ARS", last4: "0091", status: "ready", lastStatementAt: "2026-11-06" },
      ],
    },
    erp: { status: "ready", system: "Tango", mode: "export" },
  },
];

/** The tenant the client portal shows. */
export const currentTenantId = "t-agro";

const rule = (id: string, name: string, version = 1) => ({ id, name, version });

export const matches: Match[] = [
  {
    id: "m-101", tenantId: "t-agro", tray: "auto", motive: "exact", openedAt: "2026-11-03",
    summary: "Cobro de $ 184.500 de Cooperativa Agrícola Este", explanation: "Monto y CUIT exactos, fecha dentro de la ventana de 3 días.",
    movement: { date: "2026-11-03", account: "Galicia CC", description: "TRANSF RECIBIDA 30000000110", amountCents: 184_500_00 },
    document: { kind: "Factura A", number: "0003-00001207", counterparty: "Cooperativa Agrícola Este", amountCents: 184_500_00 },
    rule: rule("r-exact", "Monto y CUIT exactos"),
  },
  {
    id: "m-102", tenantId: "t-trans", tray: "auto", motive: "recurring", openedAt: "2026-11-04",
    summary: "Débito de seguro de $ 32.180", explanation: "Débito mensual con la misma referencia que confirmó el cliente.",
    movement: { date: "2026-11-04", account: "Santander CC", description: "DEB AUTOM SEGURO 4471", amountCents: -32_180_00 },
    document: { kind: "Factura B", number: "0012-00088410", counterparty: "Aseguradora Central", amountCents: 32_180_00 },
    rule: rule("r-recurring", "Débito recurrente confirmado", 2),
  },
  {
    id: "m-103", tenantId: "t-trans", tray: "auto", motive: "unique_reference", openedAt: "2026-11-05",
    summary: "Cobro de $ 58.300 por Mercado Pago", explanation: "El id de la operación de Mercado Pago figura en la factura.",
    movement: { date: "2026-11-05", account: "Mercado Pago", description: "COBRO MP 81234567890", amountCents: 58_300_00 },
    document: { kind: "Factura B", number: "0005-00002231", counterparty: "Consumidor final", amountCents: 58_300_00 },
    rule: rule("r-ref", "Referencia única"),
  },
  {
    id: "m-201", tenantId: "t-agro", tray: "suggested", motive: "tolerance", openedAt: "2026-11-02",
    summary: "Pago de $ 96.993 a Semillas Pampeanas", explanation: "La factura es de $ 97.000: $ 7 de diferencia, dentro de la tolerancia del cliente.",
    proposal: "Registrar el pago contra la factura y la diferencia como redondeo.",
    movement: { date: "2026-11-02", account: "Galicia CC", description: "TRANSF 30000000220 PAGO FC", amountCents: -96_993_00 },
    document: { kind: "Factura A", number: "0001-00004410", counterparty: "Semillas Pampeanas SA", amountCents: 97_000_00 },
    rule: rule("r-tolerance", "Tolerancia de redondeo"),
  },
  {
    id: "m-202", tenantId: "t-ferr", tray: "suggested", motive: "combination", openedAt: "2026-11-01",
    summary: "Pago de $ 250.000 a Fletes Unidos", explanation: "Un solo pago coincide con la suma de dos facturas abiertas.",
    proposal: "Imputar el pago a las facturas 0002-00000311 y 0002-00000312.",
    movement: { date: "2026-11-01", account: "Bancor CC", description: "TRANSF 30000000330", amountCents: -250_000_00 },
    document: { kind: "2 facturas", number: "0002-00000311 + 0002-00000312", counterparty: "Fletes Unidos", amountCents: 250_000_00 },
    rule: rule("r-combo", "Combinación de comprobantes"),
  },
  {
    id: "m-301", tenantId: "t-agro", tray: "exception", motive: "over_threshold", openedAt: "2026-11-02",
    summary: "Pago de $ 740.000 a Maquinarias Oeste", explanation: "La factura coincide, pero supera el umbral de $ 500.000 a partir del cual pediste firmar.",
    proposal: "Registrar el pago contra la factura 0004-00000902.",
    movement: { date: "2026-11-02", account: "Galicia CC", description: "TRANSF 30000000440", amountCents: -740_000_00 },
    document: { kind: "Factura A", number: "0004-00000902", counterparty: "Maquinarias Oeste", amountCents: 740_000_00 },
    rule: rule("r-threshold", "Umbral de firma"),
  },
  {
    id: "m-302", tenantId: "t-agro", tray: "exception", motive: "no_candidate", openedAt: "2026-10-31",
    summary: "Transferencia de $ 61.200 del 31/10 sin factura que la explique", explanation: "No encontramos ninguna factura ni partida abierta por ese monto en los 30 días anteriores.",
    movement: { date: "2026-10-31", account: "Galicia CC", description: "TRANSF 30000000550", amountCents: -61_200_00 },
    rule: rule("r-none", "Sin candidato"),
  },
  {
    id: "m-303", tenantId: "t-agro", tray: "exception", motive: "duplicate", openedAt: "2026-10-31",
    summary: "Posible pago duplicado de $ 18.450 a Servicios Gráficos", explanation: "La misma factura ya figura pagada el 24/10 desde la misma cuenta.",
    proposal: "No registrar este pago contra la factura y dejarlo para que lo revises con el proveedor.",
    movement: { date: "2026-10-31", account: "Galicia CC", description: "TRANSF 30000000660", amountCents: -18_450_00 },
    document: { kind: "Factura C", number: "0001-00000077", counterparty: "Servicios Gráficos", amountCents: 18_450_00 },
    rule: rule("r-dup", "Posible duplicado"),
  },
  {
    id: "m-304", tenantId: "t-ferr", tray: "exception", motive: "no_candidate", openedAt: "2026-10-30",
    summary: "Extracción de $ 40.000 en cajero", explanation: "Extracción en efectivo sin comprobante asociado.",
    movement: { date: "2026-10-30", account: "Bancor CC", description: "EXTRACCION CAJERO", amountCents: -40_000_00 },
    rule: rule("r-none", "Sin candidato"),
  },
];

export const files: UploadedFile[] = [
  { name: "galicia-octubre.csv", kind: "statement", uploadedAt: "2026-11-03", by: "Lucía Ferreyra", status: "ready" },
  { name: "macro-usd-octubre.xls", kind: "statement", uploadedAt: "2026-11-03", by: "Lucía Ferreyra", status: "problem" },
  { name: "galicia-octubre.csv", kind: "statement", uploadedAt: "2026-11-04", by: "Lucía Ferreyra", status: "duplicate" },
  { name: "FA-0004-00000902.pdf", kind: "document", uploadedAt: "2026-11-02", by: "Martina Gil", status: "ready" },
];
