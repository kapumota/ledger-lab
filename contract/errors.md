### Errores neutrales

Versión semántica: `1`.

Los códigos de error son parte estable del contrato. Los mensajes destinados a
personas no lo son y no deben usarse para clasificar resultados.

#### PostEntry

| HTTP | `code` | Resultado en historia | Significado |
|------|--------|-----------------------|-------------|
| 202 | - | `committed` | Solicitud aceptada y asiento `committed` |
| 400 | `malformed_body` | `rejected` | El cuerpo no es un único documento JSON válido o contiene campos no reconocidos |
| 409 | `idempotency_conflict` | `rejected` | La clave ya existe asociada a una solicitud no equivalente |
| 409 | `insufficient_funds` | `rejected` | La operación violaría I5 en una cuenta restringida |
| 422 | `invalid_entry` | `rejected` | El asiento viola las reglas de validez del comando |
| 422 | `unknown_account` | `rejected` | Al menos una cuenta no existe |
| 503 | `retries_exhausted` | `rejected` | La implementación agotó reintentos sin confirmar el nuevo asiento |
| 500 | `internal` | `unknown` | El cliente no puede inferir de forma contractual el efecto financiero final |

`idempotency_conflict` significa que esta invocación no crea un efecto financiero
nuevo. Puede existir un asiento previo asociado a la misma clave.

`retries_exhausted` se clasifica como `rejected` porque ninguna tentativa llegó a
confirmar el nuevo asiento. Un error `internal` no ofrece esa garantía y debe
tratarse como resultado incierto hasta reconciliación.

#### Códigos observacionales del arnés

Los siguientes códigos no son respuestas del runtime. Los produce el arnés al
clasificar lo que observó:

| `error_code` | Resultado | Significado |
|--------------|-----------|-------------|
| `timeout` | `unknown` | El arnés agotó su tiempo de espera sin respuesta contractual |
| `transport_error` | `unknown` | La conexión falló antes de obtener una respuesta contractual |

El arnés conserva el código estable cuando recibe una respuesta de error. No
traduce mensajes de texto ni errores específicos de Go, PostgreSQL, BEAM o
EventStore.

#### Errores de endpoints auxiliares

Los errores de `GET /accounts/{id}/balance`, `/metrics` y `/healthz` no forman
parte del resultado financiero de `PostEntry`. Pueden tener contrato HTTP propio,
pero no deben mezclarse con la historia neutral de operaciones financieras.
