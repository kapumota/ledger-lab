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

| Codigo | `code`                  | Significado |
|--------|-------------------------|-------------|
| 201    |                         | Asiento creado |
| 200    |                         | Reintento legitimo de una solicitud ya aplicada |
| 409    | `idempotency_conflict`  | Clave reutilizada con un cuerpo distinto |
| 409    | `insufficient_funds`    | La operacion dejaria negativa una cuenta restringida |
| 422    | `invalid_entry`         | No suma cero, menos de dos postings o importe cero |
| 422    | `unknown_account`       | Alguna cuenta no existe |
| 503    | `retries_exhausted`     | Conflicto de serializacion persistente |

La distincion entre 201 y 200 es informativa. El contrato exige que el efecto
final sea identico, no que el codigo lo sea.

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
| I3 | Dos solicitudes con la misma clave producen exactamente un asiento |
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
