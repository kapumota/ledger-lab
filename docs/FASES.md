### Fases del proyecto

| Fase | Contenido | Estado |
|------|-----------|--------|
| A | Contrato y modelo contable en Go y PostgreSQL | implementada |
| B | Atomicidad y concurrencia, estrategias de serializacion | implementada |
| C | Idempotencia con clave y fingerprint | implementada |
| D | Arnes neutral, verificador e inyector | implementada |
| E | Implementacion BEAM con Commanded y EventStore | pendiente |
| F | Comparacion factorial entre brazos | pendiente |
| G | Outbox con relay, broker y crash injection | parcial, solo la tabla |
| H | Reconciliacion entre ledger y stream | pendiente |
| I | Campanas reproducibles e informe | pendiente |

#### Criterios de aceptacion

**Fase A.** Property-based testing con `testing/quick` sobre generación
automática de asientos balanceados, repartos proporcionales y conservación exacta
de importes para I1. I4 se comprueba en PostgreSQL mediante pruebas de integración que intentan
UPDATE y DELETE sobre `entries` y `postings` y exigen su rechazo.

**Fase B.** Doscientas transferencias concurrentes sobre una cuenta caliente.
I2, I5 e I6 se cumplen bajo la configuracion de referencia. La anomalia
observada bajo configuraciones incoherentes queda documentada en
`docs/HALLAZGOS.md`.

**Fase C.** I3 se cumple bajo duplicacion deliberada, incluida duplicacion
concurrente de la misma clave desde veinte goroutines.

**Fase D.** El verificador detecta violaciones inyectadas artificialmente de I1,
I5 e I6. I4 no pertenece a esta falsación porque una fotografía final de la base
no permite reconstruir una mutación histórica. Su evidencia corresponde a las
pruebas de integración del esquema.

**Fase E.** El brazo BEAM pasa la misma suite de contrato que el brazo Go, sin
modificar el contrato ni el verificador.

**Fase F.** Cero violaciones en ambos brazos en toda corrida admitida. Los
resultados se reproducen desde `experiments/configs` y `experiments/raw`.

**Fase G.** I7 se cumple con crash inyectado en los cuatro puntos del ciclo de
publicacion.

**Fase H.** Toda divergencia inyectada entre ledger y stream es detectada y
resuelta o reportada. Ninguna pasa inadvertida.

**Fase I.** Una reejecucion desde cero reproduce las conclusiones dentro del
margen declarado.

#### Alcance excluido de la version 1.0

API Gateway, KYC, deteccion de fraude sobre grafos, ISO 8583, ISO 20022,
tokenizacion PCI, multi tenant y despliegue en Kubernetes. Todo ello tiene
sentido unicamente sobre un nucleo financiero demostrablemente correcto.
