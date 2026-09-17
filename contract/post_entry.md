### Contrato neutral de PostEntry

Versión semántica: `1`.

`PostEntry` registra un único asiento de doble entrada. El significado de la
operación es independiente del runtime que la ejecute.

#### Solicitud

La representación HTTP actual es:

```text
POST /entries
Content-Type: application/json
```

```json
{
  "idempotency_key": "7ea4803d-1bb1-4ad4-b923-70bb8a85af43",
  "currency": "PEN",
  "postings": [
    {
      "account_id": "1ff73504-e5de-49fb-882a-7fc09f6e06ce",
      "amount_minor": -10050
    },
    {
      "account_id": "51392d41-edc2-4a4f-8656-0ed36220a874",
      "amount_minor": 10050
    }
  ],
  "metadata": {}
}
```

La semántica de los campos es:

- `idempotency_key`: UUID que identifica la intención idempotente del cliente;
- `currency`: código de moneda de tres letras. Su forma canónica es mayúscula;
- `postings`: multiconjunto de líneas del asiento;
- `account_id`: UUID de una cuenta;
- `amount_minor`: entero con signo de 64 bits en unidades mínimas;
- `metadata`: valor JSON opcional. Si se omite, su valor contractual es `{}`.

El signo de `amount_minor` expresa la dirección contable. Un valor negativo es
cargo y un valor positivo es abono. El ledger no recibe importes monetarios en
punto flotante ni en decimal.

#### Validez

Una solicitud válida debe cumplir simultáneamente:

- `idempotency_key` válida;
- moneda válida;
- al menos dos postings;
- ningún posting con importe cero;
- todas las cuentas existentes;
- todas las cuentas pertenecientes a la moneda del asiento;
- suma exacta de `amount_minor` igual a cero;
- ausencia de desbordamiento en la aritmética entera.

La moneda mixta dentro de un asiento está excluida. Un cambio de divisa se
representa mediante dos asientos, uno por moneda, conectados por cuentas puente.

#### Equivalencia de solicitudes

I3 exige distinguir un reintento legítimo de la reutilización indebida de una
clave. Dos solicitudes con la misma `idempotency_key` son equivalentes si, tras
normalización contractual, coinciden en:

- moneda;
- multiconjunto de postings;
- metadata.

El orden de los postings no cambia la solicitud. Los postings duplicados no se
eliminan: forman parte del multiconjunto y conservan su multiplicidad.

Para UUID se usa su valor semántico, no la ortografía concreta de entrada. La
moneda se compara en su forma canónica de tres letras mayúsculas.

En metadata:

- el orden de las claves de objetos no importa;
- el espacio en blanco JSON no importa;
- el orden de los elementos de arrays sí importa;
- metadata omitida es equivalente a `{}`;
- un cambio de contenido sí cambia la solicitud;
- en la versión 1, representaciones numéricas distintas como `1` y `1.0` se
  consideran distintas para el fingerprint neutral.

#### Fingerprint neutral del arnés

El arnés puede materializar la equivalencia anterior como
`request_fingerprint`. Ese fingerprint pertenece al arnés neutral y no tiene que
coincidir byte por byte con un hash interno de Go o BEAM.

Para la versión 1:

1. los UUID se emiten en forma canónica minúscula;
2. la moneda se emite en forma canónica;
3. los postings se ordenan por `account_id` y después por `amount_minor`;
4. metadata se compacta recursivamente, ordenando las claves de objetos y
   preservando el orden de arrays y el lexema de los números;
5. se construye JSON UTF-8 sin espacio en blanco con orden fijo de campos
   `currency`, `idempotency_key`, `metadata`, `postings`;
6. se calcula SHA-256 y se representa como `sha256:<hex-minúscula>`.

Los runtimes pueden usar otra representación interna siempre que implementen la
misma relación de equivalencia contractual.

#### Resultado exitoso

La primera aplicación y un reintento legítimo producen la misma respuesta
observable:

```text
HTTP 202 Accepted
```

```json
{
  "entry_id": "a650ba53-cba7-4b57-bf9a-994d2e98b443",
  "status": "committed"
}
```

El `entry_id` debe ser el mismo para todos los reintentos equivalentes de una
misma `idempotency_key`.

El contrato HTTP no expone si la solicitud fue duplicada, el número de
reintentos internos, la estrategia de serialización ni la latencia interna.
