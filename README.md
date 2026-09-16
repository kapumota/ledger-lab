### ledger-lab

Laboratorio comparativo de arquitecturas de ejecucion para ledgers financieros
de doble entrada correctos bajo concurrencia y fallos.

Un mismo contrato financiero implementado dos veces, en Go sobre PostgreSQL y
en Elixir sobre BEAM con CQRS y event sourcing, evaluado por un arnes externo
que no pertenece a ninguna de las dos implementaciones.

El proyecto esta orientado al estudio de sistemas transaccionales financieros.
No es un core bancario listo para produccion y no pretende serlo.

#### Estado del proyecto

Estado actual, desarrollo experimental. Fases A a C implementadas en Go, arnes
de la Fase D operativo. El brazo Elixir todavia no existe.

#### La propiedad central

```
cuenta_origen       -100.00
cuenta_destino      +100.00
--------------------------------
total                  0.00
```

Si una transferencia intentara dejar `-100 + 90 = -10`, el asiento completo se
aborta. Esa verificacion no vive solo en el codigo de aplicacion, vive en un
trigger diferido del esquema, porque una invariante que solo vive en la
aplicacion deja de existir en cuanto alguien escribe por otra ruta.

#### Que implementa hoy

- Modelo contable de doble entrada con importes enteros en unidades minimas.
- Verificacion de I1 e I4 en el esquema mediante triggers, uno de ellos diferido.
- Escritura atomica con tres estrategias de serializacion configurables.
- Idempotencia con clave y fingerprint canonico de la solicitud.
- Proyeccion de saldos con verificacion de no negatividad dentro de la transaccion.
- Transactional outbox escrito en la misma transaccion que el asiento.
- Verificador externo de I1, I2, I3, I5, I6 e I7 en SQL puro.
- Inyector de violaciones deliberadas para validar el verificador.
- Generador de carga con cinco perfiles de contencion y duplicacion configurable.

#### Simplificaciones deliberadas

- No hay relay de outbox ni broker. La tabla se escribe pero nadie la consume.
- No hay reconciliacion contra el stream.
- No hay inyeccion de fallos de proceso, solo de datos.
- El brazo Elixir no existe, asi que todavia no hay comparacion que reportar.
- I6 solo se evalua en quiescencia. Durante la carga hay transacciones en vuelo.
- No hay autenticacion ni multi tenant. Es un laboratorio, no un servicio.

Estas limitaciones estan documentadas para que la evolucion del proyecto sea
verificable y no se presenten garantias que la implementacion todavia no ofrece.

#### Estructura

```
ledger-lab/
|-- contract/          especificacion del comando y de los invariantes
|-- schema/            migraciones y semillas, neutral al runtime
|-- go/                implementacion de referencia
|-- beam/              implementacion Elixir, Fase E, todavia no existe
|-- harness/
|   |-- cmd/verifier/  comprobador de invariantes, externo a ambos brazos
|   |-- cmd/workload/  generador de carga
|   `-- cmd/injector/  inyector de violaciones deliberadas
|-- experiments/       configuraciones y resultados reproducibles
`-- docs/
```

Regla arquitectonica. Los directorios `contract`, `schema`, `harness` y
`experiments` son neutrales respecto del runtime y no importan codigo de `go/`
ni de `beam/`. Si el verificador compartiera codigo con una implementacion, esa
implementacion se estaria auditando a si misma.

#### Convenciones del repositorio

- Identificadores, tipos, funciones y metodos de Go se mantienen en ingles.
- Comentarios y mensajes dirigidos a personas se escriben en espanol.
- Los terminos propios del dominio, como `posting`, `entry`, `outbox`,
  `idempotency key` y `aggregate`, se conservan cuando forman parte del
  vocabulario tecnico o de identificadores del codigo.
- La documentacion evita simbolos tipograficos innecesarios. Para relaciones
  textuales se usa `->` cuando corresponde.
- Los titulos Markdown usan `###` y los subtitulos usan `####`.

#### Arranque rapido

```
make up          # PostgreSQL, migraciones, semillas y ledgerd
make verify      # el verificador debe salir con codigo 0
make bench       # campana corta de carga
```

Sin Docker, con una base local:

```
export DATABASE_URL=postgres://postgres:postgres@127.0.0.1:5432/ledger
make migrate seed
make run
```

#### Validacion local

```
make validate
```

Ejecuta formato, analisis estatico, pruebas con detector de carreras y
compilacion de los dos modulos. Las pruebas de integracion se omiten solas si
`DATABASE_URL` no esta definida.

#### Demostrar que el verificador sirve

Un verificador que nunca reporta nada no es evidencia de correccion. Antes de
aceptar cualquier conclusion de una campana hay que demostrar que detecta
violaciones reales.

```
make verify                       # debe salir 0
make inject KIND=i5               # corrompe la base a proposito
make verify                       # debe salir 1 y nombrar I5
make reset                        # restaurar antes de medir
```

#### Trabajo pendiente

La siguiente iteracion debe priorizar correccion y evidencia antes de ampliar
funcionalidades.

1. Relay de outbox con claim, reintento y consumidor idempotente.
2. Inyeccion de fallos de proceso en los cuatro puntos del ciclo de publicacion.
3. Reconciliacion entre ledger y stream, con deteccion de divergencia.
4. Implementacion BEAM con Commanded y EventStore sobre el mismo esquema.
5. Campanas factoriales completas y su informe reproducible.
6. Evaluar snapshots de la proyeccion solo despues de cerrar lo anterior.

#### Licencia

Apache 2.0.
