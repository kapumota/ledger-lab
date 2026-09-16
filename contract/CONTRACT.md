### Contrato ledger-lab

Este archivo es la fuente de verdad del proyecto. Ambas implementaciones lo
cumplen sin modificarlo. Un cambio aqui invalida toda comparacion anterior, asi
que cualquier modificacion debe ir acompanada de la reejecucion de las campanas.

#### Comando

```
POST /entries
Content-Type: application/json

{
  "idempotency_key": "uuid",
  "currency": "PEN",
  "postings": [
    { "account_id": "uuid", "amount_minor": -10050 },
    { "account_id": "uuid", "amount_minor":  10050 }
  ],
  "metadata": { }
}
```

`amount_minor` es un entero con signo en unidades minimas de la moneda. Nunca
es decimal, nunca es punto flotante, nunca es una cadena con separador. El signo
lleva la direccion, negativo es cargo y positivo es abono.

#### Respuestas

Una solicitud aceptada devuelve siempre el mismo contrato observable, tanto en
la primera aplicación como en un reintento legítimo con la misma clave y el mismo
cuerpo:

```json
{
  "entry_id": "uuid",
  "status": "committed"
}
```

| Codigo | `code`                  | Significado |
|--------|-------------------------|-------------|
| 202    |                         | Solicitud aceptada, asiento en estado `committed` |
| 400    | `malformed_body`        | JSON mal formado o campos no reconocidos |
| 409    | `idempotency_conflict`  | Clave reutilizada con un cuerpo distinto |
| 409    | `insufficient_funds`    | La operacion dejaria negativa una cuenta restringida |
| 422    | `invalid_entry`         | No suma cero, menos de dos postings o importe cero |
| 422    | `unknown_account`       | Alguna cuenta no existe |
| 503    | `retries_exhausted`     | Conflicto de serializacion persistente |

I3 exige respuestas equivalentes para la misma `idempotency_key` y el mismo
cuerpo. Por eso la respuesta HTTP no expone si el efecto se creó en esta llamada,
cuántos reintentos internos ocurrieron ni la latencia de la operación. Esos datos
permanecen disponibles mediante las métricas del runtime.

#### Consultas

```
GET /accounts/{id}/balance   ->  { "account_id": "...", "amount_minor": 0 }
GET /metrics                 ->  contadores y percentiles de la ruta de escritura
GET /healthz                 ->  { "status": "ok" }
```

#### Invariantes

| Id | Enunciado |
|----|-----------|
| I1 | Todo asiento suma cero por moneda y tiene al menos dos postings |
| I2 | Para transferencias internas, la suma de saldos del sistema es constante |
| I3 | Misma clave y mismo cuerpo producen exactamente un asiento y respuestas equivalentes |
| I4 | Ningun asiento se modifica ni se elimina. Una correccion es un reverso |
| I5 | Las cuentas restringidas nunca presentan saldo negativo committed |
| I6 | La proyeccion de saldo coincide con el recomputo desde postings |
| I7 | Todo asiento confirmado produce al menos un evento, con efecto exactamente uno |

I1 e I4 se defienden en el esquema, no en la aplicacion. I5 es el invariante que
cruza la frontera del aggregate y su tratamiento es el factor central del
experimento. I6 solo se evalua en quiescencia.

#### Alcance excluido

Moneda mixta dentro de un asiento. Un cambio de divisa son dos asientos, uno por
moneda, unidos por cuentas puente de FX. Es la unica forma de que I1 siga siendo
decidible sobre enteros.
