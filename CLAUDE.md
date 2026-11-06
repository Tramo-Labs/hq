# Reglas del repo

- Martín habla en castellano rioplatense; se le contesta corto y a nivel producto. Nada de nombres de archivo ni de funciones salvo que lo pida.
- Código, commits y copy técnico en inglés. El copy de la landing en castellano con voseo (mercado: Argentina y LatAm).
- Commits sin firma de ninguna herramienta ni trailer Co-Authored-By.
- Nunca se publica un número que no se midió. "Ejemplo ilustrativo" hasta tener datos reales de un cliente.
- Nunca se copia código de repos de empleadores anteriores. Inspirarse en la estructura sí; pegar archivos no.
- Nada de Slack, mails ni mensajes a terceros sin que Martín lo pida: se deja el draft.
- Decisiones: se agregan a `strategy/DECISIONES.md`, append-only, con fecha y por qué.
- El team trabaja sobre `TEAM.md`: cada tarea con dueño y estado. El lead reparte; nadie toma una tarea de otro sin avisar.
- Antes de afirmar que algo funciona: correrlo. `npm run build` verde en `eng/web` antes de cerrar una tarea de landing.
- Backend en `eng/core` (Go 1.27, binario en `~/.local/go/bin`). Reglas de diseño en `eng/ARQUITECTURA.md`: spec primero, módulo con README, `Idempotency-Key`, RLS por tenant, secretos solo por entorno, la clave de ARCA solo en el gateway. `make build` y `make test` verdes antes de cerrar una tarea.
