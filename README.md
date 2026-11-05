# Tramo (nombre provisorio)

Repo nuevo, código propio desde cero. La estructura copia la idea del monorepo anterior, no su código:
`product/` (qué vendemos), `strategy/` (qué creemos y qué decidimos), `ops/` (cómo operamos), `growth/` (cómo vendemos), `eng/` (código).

- Landing: `eng/web` (Next.js 16, Tailwind 4). `npm install && npm run dev` en esa carpeta.
- Team de agentes: `./team.sh "objetivo"` abre el lead con sus teammates. `./team.sh --help` para los modos.
- Roles del team: `.claude/agents/*.md`. Reglas del repo: `CLAUDE.md`.
- Tablero compartido del team: `TEAM.md`.

## Renombrar
El nombre vive en un solo lugar: `eng/web/src/content/site.ts` (`site.name`). La carpeta se renombra con ya renombrada a `~/lionware`.
