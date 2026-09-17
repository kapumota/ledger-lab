### Invariantes neutrales

Versión semántica: `1`.

Los invariantes pertenecen al contrato financiero y no a una implementación.
La evidencia usada para evaluarlos puede diferir, pero ninguna implementación
puede redefinir su significado.

#### Invariantes

| Id | Enunciado contractual |
|----|-----------------------|
| I1 | Todo asiento tiene al menos dos postings y suma exactamente cero por moneda |
| I2 | Las transferencias internas conservan el valor total del sistema |
| I3 | Una clave idempotente y una solicitud equivalente producen un único asiento y respuestas equivalentes |
| I4 | `entries` y `postings` son append-only. Una corrección se registra mediante un asiento de reverso |
| I5 | Una cuenta restringida nunca presenta saldo negativo en un estado `committed` |
| I6 | El saldo materializado coincide con el saldo reconstruido desde postings en quiescencia |
| I7 | Todo asiento `committed` produce eventualmente el evento correspondiente y un consumidor idempotente produce un único efecto |

#### Evidencia admisible

La evidencia debe corresponder a la naturaleza del invariante.

| Id | Evidencia principal |
|----|---------------------|
| I1 | Validación del comando, defensa del esquema y verificador externo |
| I2 | Estado inicial y final observado por el verificador sobre transferencias internas |
| I3 | Historia del arnés más estado persistido e identidad del `entry_id` |
| I4 | Pruebas de integración que intentan `UPDATE` y `DELETE`; una fotografía final no prueba historia de mutaciones |
| I5 | Estado `committed` observado externamente bajo concurrencia |
| I6 | Comparación entre proyección materializada y recomputación desde postings en quiescencia |
| I7 | Evidencia de outbox, publicación y consumo idempotente cuando la fase de eventos esté implementada |

I7 permanece como invariante contractual aunque la cadena completa de
publicación y consumo todavía no esté implementada. La existencia de una fila de
outbox por sí sola no constituye evidencia completa de I7.

#### Seguridad y falsación

Para una configuración declarada correcta:

```text
violaciones esperadas = 0
```

El número de violaciones no es una métrica competitiva. Una corrida con una
violación no se considera "menos correcta": queda fuera del conjunto admitido.

Las configuraciones deliberadamente débiles pueden producir violaciones para
demostrar que el verificador es capaz de detectar estados incorrectos. Esa
falsación valida el instrumento, no convierte la seguridad en una métrica de
rendimiento.

#### Observado, probado y verificado

El contrato mantiene estas distinciones:

```text
implementado != probado
probado != formalmente verificado
observado != garantizado
```

Una conclusión experimental debe indicar qué evidencia concreta la sostiene.
