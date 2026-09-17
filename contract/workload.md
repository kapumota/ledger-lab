### Workload neutral

Versión del esquema: `1`.

Un workload describe la carga lógica que el arnés genera contra `PostEntry`.
Debe poder ejecutarse sin saber si el runtime es Go, BEAM u otra implementación
futura.

#### Configuración

La versión 1 utiliza conceptualmente:

```json
{
  "workload_version": 1,
  "seed": 17,
  "currency": "PEN",
  "profile": "hot10",
  "concurrency": 50,
  "duration_ms": 30000,
  "amount_minor": 100,
  "duplicate_pct": 50,
  "accounts": [
    "1ff73504-e5de-49fb-882a-7fc09f6e06ce",
    "51392d41-edc2-4a4f-8656-0ed36220a874"
  ],
  "funder": "4ac03740-8fb4-4bfe-8071-8a95d944e9da"
}
```

#### Campos

- `workload_version`: versión del generador neutral;
- `seed`: semilla de reproducción;
- `currency`: moneda de las operaciones;
- `profile`: perfil de contención;
- `concurrency`: número de clientes concurrentes;
- `duration_ms`: duración objetivo de la carga;
- `amount_minor`: importe positivo transferido por operación;
- `duplicate_pct`: porcentaje de operaciones que generan un segundo envío con
  la misma clave y la misma solicitud;
- `accounts`: población ordenada de cuentas de cliente;
- `funder`: cuenta usada para preparar fondos cuando el escenario lo requiere.

El orden de `accounts` forma parte de la configuración porque los perfiles
calientes y de fan-in/fan-out seleccionan posiciones concretas de esa población.

#### Perfiles de contención

`uniform` selecciona origen y destino sobre toda la población.

`hot1` concentra los orígenes en el primer uno por ciento de la población y
selecciona destinos sobre toda la población.

`hot10` concentra los orígenes en el primer diez por ciento.

`fan_in` concentra el destino en una única cuenta.

`fan_out` concentra el origen en una única cuenta.

El generador evita que una operación ordinaria use la misma cuenta como origen y
destino.

#### Duplicación deliberada

Cuando una operación es seleccionada para duplicación, el segundo envío reutiliza:

```text
idempotency_key
currency
postings
metadata
```

Por tanto, debe ser equivalente según `post_entry.md` y no producir un segundo
efecto financiero.

La duplicación del workload no es un error de transporte. Es una entrada
deliberada para evaluar I3.

#### Reproducibilidad

Para la misma versión de workload y la misma configuración, el generador debe
producir la misma secuencia lógica por cliente. El scheduling real entre clientes
puede variar y es precisamente parte de la concurrencia observada.

Cambiar el algoritmo de generación de forma que cambie la secuencia lógica exige
incrementar `workload_version`.

#### Factores que no pertenecen al workload

No forman parte de este contrato:

- URL del runtime;
- lenguaje o implementación;
- estrategia de serialización;
- nivel de aislamiento;
- presupuesto interno de reintentos;
- topología BEAM;
- perfil de fallo o punto de crash.

Esos valores pertenecen a la configuración experimental que combina un workload
neutral con un brazo de ejecución y, cuando corresponda, un escenario de fallo.
