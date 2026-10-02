# Fixtures de la fase 0

Entradas reales de `evidence/claude-P1/log.jsonl`, `evidence/codex-P1/log.jsonl`
y el segundo Stop de `evidence/{claude,codex}-P2-1/log.jsonl` en la carpeta
`agents-bridge-probe`. Solo se sustituyeron `cwd` y las rutas de transcript
por rutas de ejemplo; los tipos y demás campos se conservaron. No contienen
credenciales ni cuerpos de conversación del puente.

Las pruebas derivan comandos estáticos de estos eventos para ejercitar la
vinculación automática; también procesan los originales sin modificarlos.
PreToolUse usa `hookSpecificOutput.permissionDecision="deny"` y un motivo
no vacío, documentado para ambos harnesses:
[Claude](https://code.claude.com/docs/en/hooks) y
[Codex](https://github.com/openai/codex/blob/main/codex-rs/hooks/src/engine/output_parser.rs).
Las capacidades de apps de escritorio y Windows siguen pendientes de comprobar.
