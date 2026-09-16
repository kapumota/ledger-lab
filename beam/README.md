### Brazo BEAM. Fase E.

Todavia no implementado. Este directorio existe para fijar la interfaz y evitar
que el brazo Go adopte decisiones que despues impidan la comparacion.

#### Contrato

El mismo de `contract/CONTRACT.md`, sin ninguna desviacion. La suite de pruebas
de contrato se ejecuta contra los dos brazos en el mismo job de integracion
continua. Si el brazo BEAM necesita un cambio en el contrato, la comparacion se
invalida y las campanas anteriores hay que repetirlas.

#### Decisiones ya tomadas

El aggregate es `JournalEntry`, no `Account`. Una cuenta como aggregate
produciria dos fronteras de consistencia en una transferencia y obligaria a
introducir una saga con compensacion para resolver un problema que la eleccion
correcta de aggregate evita por completo. Ademas, Commanded recomienda que los
aggregates no consulten el estado de otros aggregates, lo que hace inviable la
verificacion de saldo desde dentro del aggregate de cuenta.

Las cuentas son proyecciones derivadas de los postings.

I5, la no negatividad de las cuentas restringidas, sigue cruzando la frontera
del aggregate porque depende del estado acumulado de una proyeccion. Ese cruce
es el objeto del experimento y admite al menos dos tratamientos en BEAM.

```
reserva por cuenta      proceso dedicado que mantiene disponible y reservado
verificacion y compensa lectura de proyeccion, despacho, reverso si falla
```

Ambos deben implementarse y medirse. Elegir uno de antemano seria decidir el
resultado antes de medirlo.

#### Pila prevista

```
Elixir 1.20
Commanded            aggregates, despacho de comandos, process managers
EventStore           persistencia de eventos sobre el mismo PostgreSQL
Ecto                 proyecciones y consultas
```

#### Lo que este brazo no cambia

La atomicidad del outbox sigue viviendo en PostgreSQL. La insercion del asiento
y de la fila de outbox ocurren en la misma transaccion. Broadway y GenStage
aportan demanda, concurrencia, supervision y reinicio del relay, que es
sustancial, pero no convierten por si solos un outbox en fiable.

#### Requisito de instrumentacion

El brazo BEAM debe exponer `GET /metrics` con los mismos campos que el brazo Go,
incluido el retardo de encolamiento en el mailbox del aggregate, que es la
metrica analoga a la tasa de reintento por conflicto de serializacion. Sin
instrumentos equivalentes no hay comparacion, hay dos mediciones distintas
puestas una al lado de la otra.
