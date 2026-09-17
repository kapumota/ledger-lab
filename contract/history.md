### Historia neutral de operaciones

Versión del esquema: `1`.

La historia es propiedad del arnés. Registra lo que el cliente experimental
observó y permanece inmutable después de escribirse. La implementación no escribe
estos registros y el verificador no los corrige retrospectivamente.

El formato de persistencia de la versión 1 es NDJSON: un objeto JSON por línea.

#### Registro

Cada invocación HTTP produce un registro independiente, incluso si es un
reintento con la misma `idempotency_key`.

```json
{
  "schema_version": 1,
  "run_id": "run-2026-09-17-001",
  "operation_id": "68de4c3e-40ae-44d9-ac4d-ea06ec23fa16",
  "client_id": "client-007",
  "client_seq": 42,
  "idempotency_key": "7ea4803d-1bb1-4ad4-b923-70bb8a85af43",
  "request_fingerprint": "sha256:0123456789abcdef",
  "invoke_ns": 123456,
  "complete_ns": 123789,
  "result": "committed",
  "entry_id": "a650ba53-cba7-4b57-bf9a-994d2e98b443",
  "error_code": null
}
```

#### Campos obligatorios

| Campo | Tipo | Regla |
|-------|------|-------|
| `schema_version` | entero | `1` para este contrato |
| `run_id` | string | Identifica una corrida experimental |
| `operation_id` | UUID | Identifica una invocación observada por el arnés |
| `client_id` | string | Identifica el cliente lógico |
| `client_seq` | entero no negativo | Orden local de invocaciones del cliente |
| `idempotency_key` | UUID | Clave enviada en `PostEntry` |
| `request_fingerprint` | string | Fingerprint neutral definido en `post_entry.md` |
| `invoke_ns` | entero no negativo | Inicio de la invocación |
| `complete_ns` | entero no negativo | Momento en que el arnés concluye la observación |
| `result` | enum | `committed`, `rejected` o `unknown` |
| `entry_id` | UUID o null | Identificador observado para un resultado `committed` |
| `error_code` | string o null | Código estable de `errors.md` o código observacional |

`complete_ns` debe ser mayor o igual que `invoke_ns`.

#### Tiempo

`invoke_ns` y `complete_ns` son desplazamientos en nanosegundos desde el origen
monotónico de la corrida, medidos por el arnés. No son timestamps producidos por
el runtime y no deben usarse como reloj global entre máquinas.

El tiempo de una invocación es:

```text
latency_ns = complete_ns - invoke_ns
```

#### Clasificación del resultado

`committed` significa que el arnés recibió la respuesta contractual `202` con un
`entry_id`.

`rejected` significa que el arnés recibió una respuesta contractual cuyo código
garantiza que esa invocación no confirmó un nuevo asiento.

`unknown` significa que, desde la observación del cliente, el efecto final no
puede determinarse. Una pérdida de respuesta después de un posible commit es el
caso principal.

Un resultado `unknown` no se reescribe después como `committed` o `rejected`.

#### Separación entre historia y verificación

El flujo es:

```text
history.ndjson
      |
      v
verifier + estado persistido
      |
      v
resultado de verificación separado
```

El verificador puede concluir posteriormente que una operación observada como
`unknown` tuvo o no efecto, pero esa conclusión se registra fuera de
`history.ndjson`.

#### Campos deliberadamente ausentes

La historia de operación no contiene:

- runtime;
- estrategia de serialización;
- nivel de aislamiento;
- número de reintentos internos;
- latencia interna del runtime.

Esos factores pertenecen a la configuración experimental de la corrida. Mezclarlos
con la historia cambiaría el contrato cada vez que cambie una implementación.
