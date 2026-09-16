### Plan factorial de campanas

Cada corrida se identifica con una etiqueta y produce un JSON de resumen y un
CSV de latencias crudas en `experiments/raw/`.

#### Factores

```
brazo          go | beam
estrategia     ordered_locks+read_committed | optimistic+serializable
perfil         uniform | hot1 | hot10 | fan_in | fan_out
concurrencia   10 | 50 | 200 | 1000
restriccion    activada | desactivada
fallo          ninguno | crash_pre_commit | crash_post_commit_pre_publish |
               crash_post_publish | reinicio_consumidor | duplicacion
```

#### Criterio de admision

Una corrida solo entra en el analisis si el verificador sale con codigo 0 al
terminar, en quiescencia. La seguridad es requisito, no metrica competitiva.

Las corridas con estrategia `weak` son la excepcion. Su proposito es producir
violaciones y se analizan aparte, como demostracion de que el verificador
detecta problemas reales.

#### Procedimiento por corrida

```
make reset
make run                      (o docker compose up)
make verify                   debe salir 0 sobre base limpia
make bench PROFILE=... CONCURRENCY=... SEED=... LABEL=...
esperar quiescencia
make verify                   debe salir 0, si no la corrida se descarta
```

#### Metricas reportadas

```
throughput sostenido
p50 p95 p99 max
tasa de aborto y reintento
retardo de encolamiento           solo brazo BEAM
tiempo de recuperacion            campanas con fallo
efectos duplicados
resultados inciertos              solicitudes sin respuesta
violaciones de invariante         valor esperado cero
```

#### Repeticiones

Tres semillas por celda, reportando mediana y rango. Una sola corrida por celda
no permite distinguir una diferencia real del ruido de la maquina.
