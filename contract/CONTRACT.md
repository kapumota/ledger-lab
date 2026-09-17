### Contrato neutral de ledger-lab

`contract/` es la fuente de verdad semántica del experimento. El contrato define
qué significa una operación financiera y qué debe observar el arnés sin depender
de Go, BEAM, PostgreSQL ni de una estrategia de serialización concreta.

Un cambio semántico en estos documentos invalida la comparabilidad con campañas
anteriores salvo que el cambio quede versionado y las campañas afectadas se
ejecuten de nuevo.

#### Documentos canónicos

- `post_entry.md`: comando `PostEntry`, equivalencia de solicitudes y binding HTTP.
- `invariants.md`: invariantes I1-I7 y evidencia admisible para cada uno.
- `errors.md`: errores estables del contrato y clasificación observacional.
- `history.md`: historia neutral e inmutable observada por el arnés.
- `workload.md`: definición reproducible de la carga, independiente del runtime.

Este archivo funciona únicamente como índice. Las reglas normativas viven en los
cinco documentos anteriores y no deben duplicarse aquí.

#### Regla de neutralidad

La separación de responsabilidades es:

```text
harness
  observa

implementación
  ejecuta

verifier
  decide
```

Por tanto:

- `contract/` no depende de código de `go/` ni de `beam/`;
- el arnés no importa paquetes de ninguna implementación;
- el verificador no importa paquetes de ninguna implementación;
- la implementación no escribe ni reinterpreta la historia observada;
- la estrategia, el aislamiento y los reintentos internos pertenecen a la
  configuración experimental, no al significado de `PostEntry`;
- sustituir completamente la implementación Go no debe requerir cambios en
  `contract/`, `harness/` ni `experiments/`.

#### Superficie auxiliar existente

Los endpoints siguientes siguen siendo útiles para operación y diagnóstico, pero
no definen la operación financiera neutral de D2:

```text
GET /accounts/{id}/balance
GET /metrics
GET /healthz
```
