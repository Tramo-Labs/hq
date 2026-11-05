# El equipo de agentes (investigación del 05/11/2026)

Datos de GitHub al 05/11. Lo marcado "inferencia" no está verificado contra fuente primaria.

## Dos capas

### a) El equipo de la empresa (trabaja sobre el repo y el negocio)
- **Local, en las máquinas de los fundadores**: Claude Code con agent teams (experimental) y el plugin ECC. Estructura copiada de `agentic-company-core`: roles en `.claude/agents/`, hooks que limitan quién delega a quién, y una base de conocimiento en markdown dentro del repo como memoria compartida. Para trabajo interactivo y features grandes.
- **GitHub, sin terminal**: `anthropics/claude-code-action` (MIT, 9.8k estrellas). Un issue con la etiqueta correcta y `@claude` abre un PR; un workflow de review en cada PR; un cron diario con reporte. Se autentica con el token de la suscripción de un fundador o con API key. Capar turnos y tiempo.
- **Rutinas en la nube**: Scheduled tasks de Claude Code (desde `/schedule`): grooming nocturno del backlog, borrador semanal de contenido, research de leads, reporte a los fundadores. Corre con la identidad del fundador y consume su suscripción.
- **Negocio**: contenido con `kostja94/marketing-skills` y el `content-engine` de ECC; soporte y aprobaciones por WhatsApp con `Hainrixz/whatsapp-agentkit` (MIT, en castellano, Meta Cloud API); si hace falta bandeja humana, Chatwoot (MIT).

### b) El runtime del producto (lo que corre para los clientes)
- **Motor de flujos**: **Temporal** (MIT) si aceptamos operar server y Postgres: aprobaciones con signals, historia inmutable como audit log, cero riesgo de licencia multicliente. Alternativa rápida en TypeScript: **Trigger.dev** (Apache 2.0) con `wait.forToken` para la aprobación humana. Kestra tiene UI de flujos, pero audit log, multitenant y RBAC son de pago.
- **Lo que NO se usa como runtime del producto**: n8n (su licencia prohíbe que los clientes configuren flujos a través de nuestro producto; sirve solo para automatizar a Tramo por dentro), Windmill (AGPL más licencia comercial para re-exponer), Inngest (SSPL).
- **Aprobaciones**: app propia en Next.js con tabla `approvals` (flujo, token, payload, estado, quién, cuándo) y dos canales: web y WhatsApp con botones. Inferencia de diseño.
- **Audit log**: tabla append-only por cliente con cada evento (entrada, salida del modelo con versión, decisión humana, timestamps), además de la historia del motor.
- **Lectura de facturas**: primero el **QR de ARCA** (obligatorio por RG 4291/2018: trae CUIT, tipo y número, importe, CAE), después la capa de texto del PDF, después OCR con `docling` (MIT) o `docext` (Apache 2.0), y recién ahí Claude con salida estructurada para líneas, IVA y matching. Marcar origen y confianza de cada dato, como hace `pmassimino/Soltec.DocParser` (referencia argentina). Validar contra la constatación de comprobantes de ARCA.

## Shortlist
| Repo | Licencia | Para qué |
|---|---|---|
| anthropics/claude-code-action | MIT | issue a PR, review, cron |
| Scheduled tasks de Claude Code | servicio | rutinas sin terminal |
| azretkenzhaliev/agentic-company-core | MIT, 0 estrellas | patrón de empresa con Claude Code; copiar la estructura, no depender del repo |
| SenteLabsAI/OpenExecutive | Apache 2.0, 5.8k | consejo ejecutivo virtual con memoria, reporta a fundadores |
| Yeachan-Heo/oh-my-claudecode | MIT, 40k | pipeline plan, ejecutar, verificar; local |
| awslabs/cli-agent-orchestrator | Apache 2.0 | supervisor y workers en tmux; ojo: salta permisos por defecto |
| temporalio/temporal | MIT | runtime del producto |
| triggerdotdev/trigger.dev | Apache 2.0 | runtime del producto, camino rápido |
| docling-project/docling, NanoNets/docext | MIT, Apache 2.0 | lectura de PDFs y facturas |
| Hainrixz/whatsapp-agentkit | MIT | agente de WhatsApp en castellano |
| kostja94/marketing-skills | MIT | 160 skills de marketing para Claude Code |

Descartados o con cuidado: MetaGPT (parado desde enero), ChatDev (investigación), CrewAI y AutoGen (frameworks, no simulan empresa), ruflo (marketing agresivo), OpenOutreach y Paperless-ngx (GPL), Evolution API (riesgo de baneo por Baileys; preferir Meta Cloud API).
