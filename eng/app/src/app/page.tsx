import { redirect } from "next/navigation";

// app.X/ is the portal: its home, which sends anyone without a session to /entrar.
export default function Page() {
  redirect("/app");
}
