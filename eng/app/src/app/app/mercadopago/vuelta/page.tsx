import type { Metadata } from "next";
import { app } from "@/content/app";
import { MercadoPagoReturn } from "@/components/app/mercadopago";

// Mercado Pago's code is in the query until the page takes it out: send no referrer anywhere.
export const metadata: Metadata = { title: app.mercadoPago.back.title, referrer: "no-referrer" };

export default function Page() {
  return <MercadoPagoReturn />;
}
