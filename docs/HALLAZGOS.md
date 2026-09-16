### Hallazgos

Registro de resultados empiricos obtenidos durante la construccion. Cada entrada
debe ser reproducible con el codigo del repositorio.

#### H1. Bloqueo pesimista bajo SERIALIZABLE degrada sin aportar correccion

Fecha, Fase B. Reproducible con `go test ./internal/store/...` fijando
`Isolation: pgx.Serializable` junto a `Strategy: StrategyOrderedLocks`.

Observacion. Con doscientas transferencias concurrentes desde una unica cuenta
caliente y la combinacion de `SELECT FOR UPDATE` ordenado con aislamiento
SERIALIZABLE, la mayoria de las transacciones agota diez reintentos y falla con
`40001`. El mensaje alterna entre `could not serialize access due to concurrent
update` y `due to read/write dependencies among transactions`.

Explicacion. Bajo READ COMMITTED, `FOR UPDATE` espera a que el poseedor del
bloqueo confirme y a continuacion reevalua la fila sobre la version mas reciente.
Bajo REPEATABLE READ y SERIALIZABLE eso no ocurre. Como la transaccion opera
sobre un snapshot fijo, encontrar la fila modificada por otra transaccion ya
confirmada no es una situacion recuperable y el motor aborta.

Consecuencia de diseno. Las dos combinaciones coherentes son estas.

```
ordered_locks + read_committed    pesimista, la exclusion la aporta el bloqueo
optimistic    + serializable      optimista, la exclusion la aporta el motor
```

Mezclarlas paga el costo de ambas y el beneficio de ninguna. La configuracion de
referencia del proyecto pasa a ser bloqueo ordenado con READ COMMITTED.

Relevancia para el experimento. Esto refuerza que el nivel de aislamiento debe
tratarse como factor y no como constante. Una comparacion Go contra BEAM que
fijara SERIALIZABLE en el brazo Go por parecer la opcion mas segura estaria
midiendo una configuracion incoherente y atribuiria a la arquitectura una
diferencia que en realidad proviene de la combinacion elegida.

#### H2. El trigger diferido es la unica defensa real de I1

Fecha, Fase A. Reproducible aplicando la migracion y ejecutando insercion
manual de un asiento desbalanceado.

Observacion. Un `CHECK` de tabla no puede expresar I1, porque la invariante es
sobre el conjunto de filas del asiento y durante la insercion el asiento esta
transitoriamente desbalanceado. Un `CONSTRAINT TRIGGER ... DEFERRABLE INITIALLY
DEFERRED` sobre `postings` lo resuelve, porque se evalua en el commit.

Consecuencia. La validacion en el dominio y la validacion en el esquema no son
redundantes. La primera da un error util al llamador, la segunda es la que
sobrevive a que alguien escriba por otra ruta, incluida la implementacion BEAM.

#### H3. El inyector es parte de la evidencia, no una herramienta auxiliar

Fecha, Fase D.

Observacion. Al inyectar una violacion de I5, el verificador la detecto y
ademas reporto una violacion de I7 que no se habia previsto, porque el asiento
inyectado no genera fila de outbox.

Consecuencia. El efecto colateral es correcto y conviene documentarlo. Un
asiento que aparece en el ledger sin haber pasado por la ruta de escritura es
exactamente lo que I7 debe delatar.
