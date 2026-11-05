#!/usr/bin/env bash
# Un solo script para levantar el team de agentes de este repo.
#
#   ./team.sh "objetivo"          abre el lead en esta terminal con agent teams
#                                 (el lead spawnea producto, frontend, ventas y qa)
#   ./team.sh --bg "objetivo"     cada rol como sesión de fondo en su worktree
#   ./team.sh --status            lista las sesiones de fondo
#   ./team.sh --stop              las para todas
#
# Requiere Claude Code 2.1+. Agent teams es experimental: se prende con la
# variable de abajo. Sin ella los roles corren como subagentes comunes.
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
export CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1
ROLES="producto backend datos frontend ventas qa"
MODO="team"
case "${1:-}" in
  --bg)     MODO="bg"; shift ;;
  --status) claude agents; exit $? ;;
  --stop)   for r in $ROLES lead; do claude agents stop "$r" >/dev/null 2>&1 || true; done; echo "paradas"; exit 0 ;;
  -h|--help) sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
esac
OBJ="${1:-$(sed -n 's/^Objetivo de la semana: //p' TEAM.md)}"
[ -z "$OBJ" ] && { echo "falta el objetivo"; exit 1; }
NOMBRE="team-$(date +%d%H%M)"

if [ "$MODO" = "team" ]; then
  exec claude --agent lead -n "$NOMBRE" --permission-mode acceptEdits \
    "Objetivo: $OBJ. Leé TEAM.md y CLAUDE.md. Spawneá un teammate por rol usando los agent types producto, backend, datos, frontend, ventas y qa. Asigná las tareas del tablero, mantenelo al día, y reportame en castellano corto cuando terminen."
fi

for r in $ROLES; do
  claude --bg --agent "$r" -n "$r" --worktree "$r" --permission-mode acceptEdits \
    "Sos el teammate $r. Objetivo: $OBJ. Leé TEAM.md y CLAUDE.md, tomá tus tareas, y avisá al lead por SendMessage (usá ListAgents para encontrarlo) cuando termines o te trabes."
done
claude --agent lead -n lead --permission-mode acceptEdits \
  "Objetivo: $OBJ. Los teammates producto, backend, datos, frontend, ventas y qa ya corren como sesiones de fondo en sus worktrees. Coordinalos por SendMessage, integrá sus ramas worktree-<rol> y mantené TEAM.md."
