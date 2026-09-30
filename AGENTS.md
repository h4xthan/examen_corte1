# AGENTS.md — DVBS: Hardening + Migración a TiDB

> **Este archivo es la fuente de verdad operativa del proyecto.**
> Revisarlo **antes de empezar cada fase y antes de cada commit**.
> Si algo de aquí contradice el código, el código gana: se corrige el código *y* este archivo.

---

## 1. Qué es este proyecto

Nació como **Damn Vulnerable Book Shop**: una tienda de libros con 18 vulnerabilidades
deliberadas para practicar hacking web. Eso terminó. El objetivo ahora es una
**aplicación de tienda de libros completamente funcional y segura** para uso universitario,
con la base de datos migrada de PostgreSQL local a **TiDB Cloud**.

**El proyecto ya no es un laboratorio de pentesting.** No queda ningún payload, exploit,
ni configuración insegura intencional en el repositorio.

---

## 2. Decisiones bloqueadas

No reabrir estas preguntas salvo petición explícita del usuario.

| Tema | Decisión | Consecuencia práctica |
|---|---|---|
| Alcance | Cerrar los 18 vulns + **panel admin funcional**. Nada más ambitious. | No añadir pasarela de pagos, ni multi-tenant, ni carrito invitado. |
| Sesión | **Cookie httpOnly + CSRF token** (double-submit) | El token **no** vive en `localStorage`. Obliga a migrar `client.js` y el `fetch` crudo de `BookDetail.jsx`. |
| Base de datos | **TiDB Cloud Starter** (free) | Obliga a `ConnMaxLifetime ≈ 5 min` (AWS corta ociosos a 340 s). No se puede indexar `TEXT`. |
| Orden | **Seguridad primero sobre PostgreSQL, luego migrar** | `internal/store/` se toca dos veces. Aceptado explícitamente. |
| Tests | **Reescribir** los 2.082 líneas actuales como tests de regresión | Los tests que afirman los bugs se invierten; no se borran sin reemplazo. |
| Material de ataque | **Borrar todo** | Sin README de lab, sin `/walkthrough`, sin `vulns.js`, sin `/inbox`. |
| Roles | Cuatro: `admin`, `capturista`, `auditor`, `customer` | Spec del profesor. `RequireRoles` en el router; el gate de admin/sesión consulta la fila real. **El catálogo lo captura solo el `capturista`** (`POST/PUT/DELETE /books`); el `admin` ya no toca libros (solo lectura). El `auditor` ve **todo** — listados, stats, usuarios, reseñas, órdenes ajenas y la lista de respaldos — pero no escribe nada ni descarga respaldos (el dump tiene hashes/tokens). El `customer` es el único que usa checkout/compra. |
| Canal de email | Interfaz `Mailer`: backend `SMTP` en prod, backend `Log` en dev | El código de reset **nunca** vuelve por HTTP. |
| Endpoint de datos | **El panel admin va por TiDB Data Service**, no por el pool. El navegador **nunca** lo llama: la private key se queda en Go. | `React → Go → Data Service → TiDB`. El frontend no cambia una línea. Obliga a un store compuesto: CRUD por HTTP, stock por SQL. Ver §3.7. |
| Backend del catálogo | **Solo TiDB Data Service**. `BOOKS_BACKEND` eliminado: el catálogo entero del panel y de la tienda sale de los endpoints, con stock y conteo por SQL | Quitar el fallback `sql` al arrancar (la tienda y el panel pasan por los 5 endpoints de la Fase 10; no hay segunda fuente). Ver §3.7 y §3.10. |
| Identificadores | **Todo id cruza el JSON entrecomillado**, en respuestas y en los DTO que lo reciben | `AUTO_RANDOM` da ids de 19 dígitos, que un `float64` no representa. `json:",string"`. El dinero sigue siendo número. Ver §3.8. |

---

## 3. Hallazgos técnicos (leer antes de tocar nada)

Estos son los puntos que hacen perder horas si no se tienen presentes.

### 3.1 Estructura del proyecto

- **55 rutas** en `internal/router/router.go`. **24 no verifican propiedad**: solo 4 leen
  `UserIDFrom(ctx)` (`couponHandler.apply`, `.Applied`, `.RemoveApplied`, `checkoutHandler.Create`).
- **6 rutas sin autenticar**, incluidas `POST/PUT/DELETE /books` → escritura anónima del catálogo.
- **Cero transacciones en todo el repositorio.** `db.Queries.WithTx` existe generado y nunca
  se usa. Los fixes de #14 y #18 exigen transacciones reales: es la primera vez que aparecen.
- `internal/transport/test_main_test.go:73-130` **re-declara las 55 rutas a mano**. Cualquier
  cambio de routing debe replicarse ahí o los tests miden cableado viejo. Hay que borrarlo y
  que use `router.New(...)`.
- Los **2.082 líneas de tests afirman los bugs** (`TestForgedTokenGrantsAdminAccess` espera
  200, `TestCouponRaceConditionMaxUsesOne` *exige* que la carrera se reproduzca). Fallarán al
  arreglarlos: es el comportamiento esperado, no un error.
- Los tests corren contra la **base real sin reset de esquema, sin rollback y sin fixtures**.
  Las filas se acumulan entre ejecuciones. Arreglarlo antes de la Fase 8.
- `internal/store/*.go` usan **9 interfaces distintas** (`store.Store`, `store.User`, ...),
  una por tabla. Todas reciben ya el `context.Context` de la petición (ver §4.7).

### 3.2 Auth y sesión

- `middleware.JWTSecret` (`internal/middleware/auth.go:12`) es una **constante exportada**.
  Además la consume `auth_service.go:95` y el propio test `admin_handler_test.go:39` para
  falsificar tokens. Dejar de exportarla no basta: hay que sacarla del paquete.
- El keyfunc de `auth.go:31` **devuelve el secreto para cualquier `alg`**. Fijar
  `jwt.WithValidMethods([]string{"HS256"})` y validar `iss`/`aud`.
- Expiración actual: **7 días** (`auth_service.go:95`). Bajar a acceso corto.
- `AdminOnly` (`internal/middleware/admin.go:7`) confía **solo** en el claim `role`. Con
  cookie hay que consultar la fila real de `users` en cada request admin.
- `decodeJSON` (`book_handler.go:50`) no tiene `DisallowUnknownFields` ni límite de tamaño.
  Es la puerta por la que entran `role` y `balance_cents`.
- `writeError`/`handleStoreError` (`book_handler.go:32-48`) devuelven `err.Error()` crudo al
  cliente, incluidos errores de BD. `Login.jsx:41` lo muestra en pantalla.

### 3.3 Modelo de datos

- `orders.total` es `NUMERIC(10,2)` (float) mientras `users.balance_cents` es `BIGINT`
  (enteros). `checkout_service.go:57` hace `int64(finalTotal)`, **truncando**. El arreglo
  mueve todo el dinero a **centavos enteros**.
- La regla "una reseña por libro y usuario" **solo vive en Go** (`review_service.go`) y es
  susceptible a carrera. Necesita `UNIQUE (book_id, user_id)`.
- No hay ningún `CHECK` en el esquema: `rating`, `quantity`, `discount_percent`, `max_uses`
  aceptan lo que se les mande.
- `password_reset_tokens.token` **no tiene índice ni UNIQUE**. Con el token aleatorio nuevo
  hay que indexarlo.
- `coupons.code` es `TEXT` con `UNIQUE`. En MySQL/TiDB eso no es indexable sin prefijo:
  habrá que migrarlo a `VARCHAR`.
- `admin_service.go:Stats` carga **todas las filas de todas las tablas** solo para contar, y
  su `TotalOrderValue` suma totales puestos por el cliente.

### 3.4 Frontend

- **`dangerouslySetInnerHTML` NO existe en el código actual.** `docs/PLAN.md:107` está
  desactualizado y **no debe usarse como referencia**. El XSS real es el `<object>` de
  `BookDetail.jsx:258-269`, que carga el SVG como documento same-origin.
- **CSP con `unsafe-inline` no va a funcionar sin más.** Vite inyecta `<script type="module">`
  inline y estilos inline en producción. Hay que medir `dist/index.html` antes de fijar la
  política: `default-src 'self' 'unsafe-inline'` es un no-op de seguridad y deshonesto.
  Decidir con el HTML realmente generado, no asumiendo.
- Google Fonts se carga de un third party sin `integrity`. A self-hostear o aceptar el
  riesgo documentado.
- `client.js` no tiene interceptor de 401. Al migrar a cookie + CSRF es buen momento para
  añadirlo e introducir un `AuthContext` que elimine los `window.location.reload()` de
  `login`/`logout` (existen porque no hay contexto de auth).
- `vite.config.js:12-21` tiene un hack de `bypass` para `/inbox` y `/admin` porque la
  navegación SPA chocaba con la API. Al borrar `/inbox`, **solo queda `/admin`**.
- El cliente escribe `user_id: user.id` en el body de reviews y checkout
  (`BookDetail.jsx:108`, `Cart.jsx:86`). Aunque el backend debe ignorar ese campo, el
  cliente debe dejar de enviarlo.
- El token se guarda en `localStorage` (`client.js:6`) y `BookDetail.jsx:90` lo lee crudo para
  el `fetch` del upload. Ambos deben migrarse a cookie.

### 3.5 Bugs funcionales reales (arreglar de paso)

- `Cart.jsx:77` llama a `setCart`, que **no existe** (`cart` es una `const`, no state) →
  `ReferenceError` al pulsar "Quitar".
- `Profile.jsx:37` usa `href="#/login"` (sintaxis de HashRouter) en una app con `BrowserRouter`.
- `middleware/logging.go`: `statusRecorder` no implementa `Flusher`/`Hijacker`/`Pusher`/`ReaderFrom`.
- No hay ruta 404 en el backend; lo desconocido cae en el default de `http.ServeMux`.
- `docker-compose.yml:11` monta el SQL en `/docker-entrypoint-initdb.d/`, que es específico de
  la imagen de Postgres. Contra TiDB gestionado **no existe ese hook**.

### 3.6 Migración a TiDB — trampas confirmadas contra la doc oficial

- **`INSERT ... RETURNING` no existe en TiDB.** El soporte del parser está mergeado, pero el
  del executor sigue en revisión. Las **9 queries `Create*`** que hoy usan `RETURNING *`
  pasan a `INSERT` + `SELECT` por `LastInsertId()`. **Es el mayor coste de la Fase 9** y
  sqlc no genera ese patrón: hay que escribirlo a mano.
- `sqlc` **sí** soporta `engine: "mysql"` y `sql_package: "database/sql"`, pero solo para el
  dialecto, no para el patrón `LastInsertId`.
- **Conexiones de Starter:** la doc oficial advierte que las conexiones abiertas más de
  30 minutos se cierran, y que el endpoint público de AWS corta ociosas **a los 340 s sin
  TCP keep-alive**. Obligatorio `SetConnMaxLifetime(5 * time.Minute)`.
- **Starter no soporta el servicio Data Migration.** Para llevar datos reales hay que usar
  TiDB Cloud CLI (`ticloud`) o Dumpling + TiDB Lightning.
- **Starter no tiene auditoría** ni maintenance window. 5 GiB de fila y 50M RU/mes de cuota
  gratuita: de sobra para este proyecto.
- **FKs**: soportadas desde v6.6, GA desde v8.5, y **no figuran entre las limitaciones de
  Starter**. Aun así la capa de servicio sigue validando propiedad explícitamente: no
  depender de la integridad de la base para seguridad. Los borrados en cascada los resuelve
  el servicio, no `ON DELETE CASCADE`.
- **`AUTO_INCREMENT` no se puede añadir con `ALTER TABLE`**: hay que definirlo en el `CREATE`.
  `AUTO_RANDOM` (nativo de TiDB) daría IDs no secuenciales, lo que complica leer las URLs.
  Para este proyecto, `AUTO_INCREMENT` es más simple.
- `TIMESTAMP` en TiDB corta en **2038**. Usar `DATETIME(3)`.
- TLS: Starter lo exige. El CA de Let's Encrypt (ISRG Root X1) está en el trust store del
  sistema, así que `mysql.RegisterTLSConfig("tidb", &tls.Config{MinVersion: tls.VersionTLS12,
  ServerName: host})` basta, sin ficheros de certificado.

---

### 3.7 TiDB Data Service — lo que hay que saber antes de mover un método

Data Service es un API HTTPS delante del clúster donde cada endpoint es una
sentencia SQL escrita a mano. Trae la clave (`ReadAndWrite` de un Data App, no la
de la organización) y devuelve un sobre `{type,data:{columns,rows,result}}`.
Todo esto está medido contra la doc y contra la consola, no supuesto:

1. **Un fallo puede llegar con HTTP 200.** El error va en `data.result.code`, y
   su ausencia de enrutado se ve en los dos sitios: `deployed endpoint not found`
   con `code: 404` dentro del sobre. Un cliente que solo mira el status llama
   éxito a un `DELETE` rechazado. `dataservice.send` **comprueba los dos**, por
   eso `Query` devuelve error y no filas vacías.
2. **Todo valor vuelve como texto**, incluidos los números. `dataservice.Row` lo
   convierte y **falla si no puede**: una columna nula donde va un entero tiene
   que ser un error, no un cero. Un libro a 0 céntimos es un bug que acaba en
   una factura.
3. **La tabla de libros usa `AUTO_RANDOM`**, así que el id no se conoce antes de
   escribir y no cabe exacto en un `float64`. Va **como texto en las dos
   direcciones**: un id enviado como número se redondea al salir.
4. **Los verbos están fijados al desplegar y no siguen el REST del panel.**
   `PUT` es el `INSERT` y `POST` es el `UPDATE`. Confundirlos duplicaría el ISBN
   en cada alta.
5. **`GET`/`DELETE` llevan los parámetros en el query, `POST`/`PUT` en el body
   JSON.** No es elección del que llama.
6. **No hay transacciones.** Data Service ejecuta cada endpoint en su propia
   conexión, así que **no se puede sumar a una transacción local**. De ahí la
   tabla de abajo, que es la regla que más cara sale si se ignora.
7. **Una sentencia que referencia un parámetro que no se envió es rechazada.**
   Por eso la portada vacía viaja como `""` y la convierte el `NULLIF` del
   endpoint, en vez de omitirse y depender de cómo se declararon los parámetros.
8. **Vuelve el resultado de la primera sentencia real del script, no el de la
   última.** `USE dvbs;` no cuenta, y lo que sigue manda: un endpoint `SELECT`
   devuelve filas, y uno `INSERT`/`UPDATE`/`DELETE` devuelve `row_affect` y **cero
   filas aunque su script termine en un `SELECT`**. Medido, no supuesto. Por eso el
   alta son **dos llamadas**: insertar y leer el libro de vuelta por `GET /book`.
   Y no es un `INSERT ... RETURNING`, que en TiDB 8.5.3 es **error de sintaxis**
   (§3.6 lo decía, y ahora está confirmado ejecutándolo).
9. **Hay tope de filas por endpoint** y un `SELECT` sin `ORDER BY` no tiene orden
   definido: el catálogo se trunca en silencio. El tope de `GET /books` está
   **medido en 1000**, no en el 2000 por defecto de la doc, así que
   `endpoints.txt` lleva `ORDER BY id LIMIT 1000` y los dos números coinciden a
   propósito: el menor de los dos es el que manda y el otro es decorado. Subir
   uno obliga a subir el otro.
10. Los conflictos de integridad son **1451** (un padre con hijos: borrar un libro
   que está en un pedido) y **1062** (índice único). 1451 y 1452 **no** son
   sinónimos: 1452 es un hijo sin padre. `store.IsDuplicate` / `store.IsForeignKey`
   reconocen los dos transportes, porque si no el mismo ISBN repetido sería un
   409 por un camino y un 500 por el otro.
11. **Un data source enlazado a medias es el 403 que más rato cuesta.** Si el
   Data App se crea o enlaza mientras está `unavailable`, el enlace queda visible
   en la consola pero invisible para la capa de permisos: **toda** llamada
   contesta 403 `Permission denied to execute DQL statement` y da igual la key que
   se use, porque el rol de la key se resuelve contra los data sources enlazados
   y no hay ninguno utilizable. Lo que sí funciona, y sirve para descartarlo todo
   lo demás: una key falsa da 401, y `row_affect`/`SELECT` de un endpoint ya
   desplegado devuelven 200. **La cura es quitar el enlace y volver a ponerlo**,
   no cambiar de key.

**El corte, que es la decisión que hay que respetar:**

| Pasa por Data Service | Se queda en SQL, y por qué |
|---|---|
| `GetAllBooks`, `GetBookByID`, `CreateBook`, `UpdateBook`, `DeleteBook` | Lectura y escritura de una sola sentencia, sin nada que las ate. |
| — | `DecrementBookStock`, `IncrementBookStock`: se llaman **dentro** de la transacción del checkout, junto al débito y la orden. Por HTTP commitearían aparte y un `ROLLBACK` de las otras dejaría stock descontado sin venta detrás. |
| — | `CountBooks`: es un agregado de panel, no parte de la lectura del catálogo, y el resto de las estadísticas ya salen del pool. Un endpoint de más que desplegar y mantener. |
| — | Checkout, `order_items`, reset de contraseña, `ResolveSession`. |

`DataServiceBookStore` es por eso un **compuesto de dos transportes**, y el
costurero está donde toca: `NewDataServiceBookStore(crud, stock)`, donde `stock`
es un `db.Querier` porque dentro de `NewQueriers` es la transacción, y eso es lo
único que hace seguros los dos métodos de stock. `TestStockNeverTravelsOverHTTP`
sale de verdad a TiDB y **exige cero llamadas HTTP**.

**Estado verificado contra el Data App real** (medido, no esperado):

- Los cinco endpoints están desplegados y enrutados (`type: "sql_endpoint"`).
- `GET /books` → 200, 200 filas, ordenadas por id, todo como texto.
- `GET /book` → 200 por id **y por isbn** (acepta los dos, `NULLIF` en ambos).
- `PUT` → 200 con `row_affect`, y el alta completa inserta y lee la fila de vuelta.
- `POST` → 200 con `row_affect`.
- `DELETE` → 200 con `row_affect`.
- ISBN duplicado → **1062**, que el store traduce a 409.
- `GET /books` con tope real de 1000 filas.
- El catálogo tiene **200 libros**, que es lo que siembra `cmd/seed` (10 autores ×
  20 títulos, ISBN `9780000001`..`9780000200`).
- Recorrido completo del panel con `BOOKS_BACKEND=dataservice` y el store Go de
  verdad: alta 201, duplicado 409, edición 200, id inexistente 404, borrado 204,
  y el borrado comprobado 404 después.

### 3.8 Identificadores: TiDB da ids que JavaScript no puede representar

`AUTO_RANDOM` produce enteros de 19 dígitos (`4035225266124144421`). El entero
más grande que un `float64` representa exactamente es 2^53, y ese id está ocho
órdenes de magnitud más allá. La cadena que lo rompía es invisible en Go:

```
Go marshalea int64  ->  JSON number  ->  JSON.parse del navegador lo vuelve float64
                    ->  4035225266076570000  ->  el panel manda ese id de vuelta
```

§3.7 lo resolvió a medias: `dataservice.Row` convierte el texto a `int64` y los
parámetros viajan como cadena. Lo que faltaba era **el último tramo**, el
`model.Book` que sale hacia el navegador. Por eso crear un libro funcionaba (no
hace falta el id) y editarlo o borrarlo no: el panel recibía un id redondeado.

**Decisión: todo identificador cruza el cable entrecomillado.** `json:",string"`
en `id`, `user_id`, `book_id` y `order_id` de todos los modelos, y también en
los DTO que **reciben** un id del cliente (`service.CheckoutItem`,
`transport.AddItemRequest`), porque el cliente devuelve lo que el servidor le
dio. **El dinero no se toca**: `price_cents`, `total_cents`, `balance_cents`,
`stock` y `pages` siguen siendo números, y `TestEveryIdentifierSurvivesJSON`
falla si alguien entrecomilla uno de ellos.

Dos trampas que esto abrió y que hubo que cerrar en el mismo sitio:

- `web/src/lib/cart.js` pasaba los ids por `Number()`, que volvería a corromper
  lo que el servidor acababa de proteger. Ahora `toId` los conserva como texto,
  y `addToCart`/`removeFromCart`/`setQuantity` normalizan en la entrada: si no,
  un id numérico fallaba en silencio y `removeFromCart` no borraba nada.
- `cart.js` es la única entrada que el usuario posee entero. Un carrito guardado
  antes del cambio tiene ids numéricos, y `String(n)` devuelve los dígitos con los
  que se guardó. Uno guardado cuando ya estaban corruptos no se puede
  recuperar: esa línea se descarta y el libro no aparece. Es la única salida
  honesta; la otra sería mandar un id redondeado a checkout.

### 3.9 Tres bugs que el navegador encontró y las pruebas no

Los tres son de la migración a TiDB o anteriores, y los tres estaban **verdes**
en el código. Ninguno se hubiera visto sin usar el panel de verdad.

1. **`/admin/stats` con 500.** MySQL ensancha `SUM()` sobre una columna entera a
   `DECIMAL` para que no desborde, y el driver lo devuelve como `[]byte`.
   `SumOrderTotals` tenía un `switch` sobre `int64`, `int` y `float64` — ninguno
   de los tres es lo que llega — así que reventaba con `unexpected type for
   sum: []uint8` y se llevaba por delante el panel entero. El arreglo es el
   `CAST(... AS SIGNED)` en la query, que hace que TiDB devuelva BIGINT; el
   `switch` ahora acepta **solo** `int64` y el error dice cuál es el arreglo si
   alguien quita el CAST. `float64` estaba en ese `switch` justamente para
   tragarse el problema, y es la razón de que un número de dinero tivesse una
   rama flotante en el camino.
2. **Borrado que no borra y dice que sí.** El endpoint `DELETE` responde 200 con
   `row_affect: 0` para un id que no está, y `DeleteBook` reenviaba eso como
   éxito. El panel Contestaba 204, decía "borrado", y el libro volvía en la
   siguiente carga. **No había error en ninguna parte**, que es la peor forma que
   puede tener un borrado. Ahora `DeleteBook` pide `row_affect` y devuelve
   `sql.ErrNoRows`, que `handleStoreError` ya traducía a 404.
3. **`ResolveSession` no rellenaba `UserID`.** `middleware.Auth` mete el
   `SessionInfo` en el contexto y `UserIDFrom` rechaza un id cero, así que
   **toda** ruta cuyo handler pregunta quién llama contestaba 401 a una petición
   que acababa de autenticarse. El gate de admin seguía funcionando porque lee
   el rol, que sí se rellenaba, y por eso el fallo parecía selectivo: el panel
   cargaba y checkout, reseñas, órdenes, direcciones y tarjetas rechazaban una
   sesión válida. `UserID` se copia ahora de la fila, en los dos sitios que
   construían el `SessionInfo`.

**Lo que enseña esto:** un `switch` de tipos que acepta `float64` en una ruta de
dinero, y un `DELETE` que no mira `row_affect`, no son detalles. Son las dos
formas que tiene un backend de mentir con la mayorita.

### 3.10 La guarda que silenciosamente valió cero: fallos que solo la suite de transporte destapó

Los de §3.9 los encontró el navegador. Estos los encontró la suite **verde**
contra TiDB de verdad, y son los que explican por qué `TestBalanceCannotGoNegative`
y media docena más de la Fase 3 fallaban en nube pero no en el papel.

1. **`DecrementBookStock` y `DebitUserBalanceIfSufficient` guardaban todos los
   cargos como `>= 0`.** La migración a MySQL le dio a cada `UPDATE ... AND x >= ?`
   dos parámetros (el `SET` y el suelo del `WHERE`), y sqlc los genera como dos
   campos de la struct de params (`Stock`/`Stock_2`, `BalanceCents`/`BalanceCents_2`).
   Los stores **solo llenaban el primero**, así que el suelo viajaba como 0 y
   `balance_cents >= 0` / `stock >= 0` matcheaban toda fila: el débito **siempre
   procedía** fuera cual fuera el saldo (un checkout con saldo 0 quedaba en
   -4500), y el stock se podía vender hasta negativo. El UPDATE "condicional" era
   un UPDATE a secas. El `UPDATE` está en la base, el bug estaba en el
   `db.*Params{...}` que el store construía. Reproducible con tres sentencias SQL
   crudas (siempre 0 filas) frente al mismo a través del store (siempre 1).
   **Es la misma raíz para el rojo supuestamente "conocido" de la carrera de
   saldo**: la carrera de verdad existía, pero además el suelo no guardaba nada,
   así que incluso secuencialmente un saldo insuficiente compraba. Fix en
   `user_store.go` y `book_store.go` (+ el `DataServiceBookStore`), rellenando el
   campo `_2`. Cualquier query nueva con `... AND x >= ?` exige comprobar que el
   store llena **ambos** parámetros.
2. **`orders.status` perdió su CHECK en la consolidación TiDB.** El
   `000006_order_status_check` de PostgreSQL no se llevó al `000001_init.up.sql`
   consolidado: una columna `VARCHAR(50)` sin vocabulario cerrado. El test de
   transporte lo destapó; la migración vuelve a declarar
   `orders_status_check`. (El hallazgo honesto tras esto es §3.10-3.)
3. **TiDB Cloud Starter no impone CHECK constraints, y no hay forma de encenderlo.**
   `@@tidb_enable_check_constraint` es `0` (GLOBAL, no hay versión de sesión) y
   `SET GLOBAL tidb_enable_check_constraint = ON` contesta 1227 (hace falta
   `SUPER`/`SYSTEM_VARIABLES_ADMIN`). Los CHECK del DDL son **documentación
   declarativa**: el enforcer real es el servicio, y `reviews_rating_range` ni
   siquiera aparecen en `information_schema` con la feature apagada. Los tests
   que afirmaban la garantía de base (`TestRatingIsBoundedInTheDatabase`,
   `TestOrderStatusIsRestrictedAndClosed`) se adaptaron: verifican que la
   plataforma tiene los CHECKs apagados (`requireCheckConstraintsUnenforced`) y
   dejan que sus bucles HTTP prueben el enforcer real. Los `UNIQUE` sí se aplican
   (`reviews_one_per_book_and_user`), por eso la regla "una reseña por libro y
   usuario" sigue siendo un índice.

**Lo que enseña esto:** sqlc regala estructuras `Params` con campos que el
migrations→generated code no rellena solo, y un `RowsAffected` que no se mira es
un `UPDATE` incondicional disfrazado. La segunda forma que tiene un backend de
mentir es **una guarda en el `WHERE` cuyo valor de referencia viaja como 0**.

---

## 4. Mapeo de vulnerabilidad → fix

Las 18 del README más la #5 de `vulns.js`. Usar como checklist durante las Fases 1-6.

| # | Vulnerabilidad | Fix | Archivo principal |
|---|---|---|---|
| 1 | `role` arbitrario en registro | DTO explícito, ignorar `role` | `service/auth_service.go` |
| 2 | Secreto JWT hardcodeado | `JWT_SECRET` por env, fail-fast, pin de `alg`, `iss`/`aud` | `middleware/auth.go` |
| 3 | IDOR en users/orders | Helper `authorizeOwner`, 404 si no es dueño | `transport/*_handler.go` |
| 4 | Enumeración de usuarios + sin rate limit | Respuesta uniforme + token bucket por IP | `transport/auth_handler.go`, `middleware/ratelimit.go` |
| 5 | Listados globales (`GET /users`, `GET /orders`) | `AdminOnly`; el cliente usa `/users/me` | `router/router.go` |
| 6 | Precio controlado por cliente | Recálculo server-side en centavos enteros | `service/checkout_service.go` |
| 7 | Comprar en nombre de otro | `user_id` solo del JWT | `service/checkout_service.go` |
| 8 | `unit_price` y `quantity` del cliente | Cargar precio del libro, validar cantidad | `service/order_item_service.go` |
| 9 | IDOR en order items | Queries con scope de propietario | `store/queries/order_items.sql` |
| 10 | Reseñas: rating, autor, sin compra | `CHECK 1-5`, autor del JWT, exigir orden pagada, `UNIQUE(book_id,user_id)` | `service/review_service.go` |
| 11 | Cualquiera crea cupones + valores absurdos | `AdminOnly` + validación de rangos | `transport/coupon_handler.go` |
| 12 | PAN/CVV en claro + listados globales | `DROP cvv`, guardar solo marca/últimos 4, nunca devolver PAN | `migration/`, `model/payment_method.go` |
| 13 | Reset de contraseña: `math/rand`, sin expiry, reutilizable, buzón abierto | `crypto/rand`, validar `expires_at`, single-use, revocar sesiones, borrar `/inbox` | `service/auth_service.go` |
| 14 | Carrera en canje de cupón | Transacción + `UNIQUE(coupon_id,user_id)` + `UPDATE ... WHERE used_count < max_uses` | `service/coupon_service.go` |
| 15 | CORS `*` | Allowlist desde `APP_ORIGIN` + credenciales + `Vary: Origin` | `middleware/cors.go` |
| 16 | Admin confía en el claim `role` | Consultar el rol en BD | `middleware/admin.go` |
| 17 | XSS almacenado por SVG | Magic bytes, re-encode, sin SVG, `<img>`, CSP | `transport/upload_handler.go`, `BookDetail.jsx` |
| 18 | Saldo: TOCTOU + mass assignment | Débito atómico condicional, quitar `balance_cents` del DTO | `service/checkout_service.go`, `store/user_store.go` |

### Invariantes que no se deben romper

1. Ningún handler acepta un `{id}` de la ruta sin comprobar que el recurso pertenece al
   sujeto del JWT. Ausencia de recurso y recurso ajeno devuelven **404**, nunca 403, para no
   filtrar existencia.
2. Todo importe monetario se maneja en **centavos enteros**. Nunca `float64` para dinero.
3. Ningún secreto (JWT, SMTP, DB) vive en el código. `Load()` falla rápido si falta.
4. Datos de tarjeta de un usuario nunca se devuelven a otro ni se exponen en listados globales.
5. Todo error de BD se loguea server-side; al cliente solo un mensaje genérico.
6. Un upload nunca se sirve con un content-type derivado de la extensión que envió el cliente.
7. Todo store recibe el `context.Context` de la petición, nunca `context.Background()`.
   **Cumplida.** Ver "Propagación de contextos" en §5, Fase 8.

---

## 5. Fases

### Fase 0 — Cimientos y limpieza — **COMPLETADA**
- [x] `internal/config/config.go`: lista completa (`JWT_SECRET`, `APP_ORIGIN`,
      `COOKIE_SECURE`, `UPLOAD_DIR`, `SMTP_*`, `ADMIN_EMAIL`, `ADMIN_PASSWORD`,
      `DB_MAX_CONNS`, `BCRYPT_COST`). `Load()` acumula todos los errores con
      `errors.Join` y **falla ante valores malformados**: `getEnvInt`/`getEnvBool`
      ya no hacen fallback silencioso. Cobertura en `internal/config/config_test.go`.
- [x] `cmd/server/main.go`: `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`,
      `IdleTimeout`.
- [x] `decodeJSON`: `http.MaxBytesReader` (1 MB) + `DisallowUnknownFields` +
      rechazo de datos adicionales. Errores 5xx genéricos en `transport/errors.go`.
- [x] Middleware de recuperación de panics (`internal/middleware/recovery.go`),
      con tests propios.
- [x] **Borrado** `web/src/data/vulns.js`, `web/src/pages/Walkthrough.jsx`, la ruta
      `/walkthrough` y su preset de nav, y el bloque CSS `.walk*`.
- [x] **Borrado** `internal/mail/mail.go`, `internal/transport/inbox_handler.go`, su
      ruta, `web/src/pages/Inbox.jsx`, la ruta `/inbox`, el enlace del topbar y
      el de `Profile.jsx`. Sustituido por `internal/mailer/` (SMTP + Log).
- [x] `cmd/seed/main.go`: el admin deja de imprimirse a los logs; se crea desde
      `ADMIN_EMAIL`/`ADMIN_PASSWORD` solo la primera vez.
- [x] `README.md` reescrito como README de app normal (sin disclaimer de laboratorio).
- [x] `docs/PLAN.md` reescrito como documento de arquitectura.
- [x] `web/nginx.conf`: `client_max_body_size` de 12m → 2m.
- [x] `internal/transport/test_main_test.go`: **el `TestMain` ya no re-declara las
      55 rutas**; usa `router.New(...)`. Esto obligó a mover la suite a
      `package transport_test` (ciclo de imports: un test in-package no puede
      importar `router`, que importa `transport`). Ningún test usaba
      identificadores no exportados, así que el cambio fue mecánico.
- [x] `recordingMailer` sustituye a `GET /inbox` en las pruebas de reset: los
      tests leen el código del correo en memoria, sin reintroducir un buzón
      legible por HTTP.

**Correcciones colaterales encontradas durante la fase:**
- `statusRecorder.WrittenHeader()` devolvía el 200 inicializado aunque no se
  hubiera escrito nada, así que `Recovery` creía la respuesta ya comprometida y
  **no escribía el 500**: un panic se servía como 200 con cuerpo vacío. Ahora
  devuelve 0 antes de escribir. Regresión cubierta en `recovery_test.go`.
- `Recovery` tenía un comentario que afirmaba que sin él "la caída mataba el
  servidor". Es falso: net/http ya recupera panics por conexión. Corregido.
- `mailer`: `smtp.DialTLS` no existe en `net/smtp`; el TLS implícito (465) se
  hace con `tls.Dial` + `smtp.NewClient`.
- `config.SMTPConfig` y `mailer.SMTPConfig` eran structs duplicados. `config`
  importa `mailer` y usa su tipo; `mailer` no importa `config`, no hay ciclo.

**Verificación:** `go build ./...` y `go vet ./...` en verde. `go test ./internal/config/
./internal/middleware/` en verde. `npm run build` en verde. La suite de
`internal/transport` **compila** (verificado con `go test -c`) pero **no se ha
ejecutado**: requiere PostgreSQL levantado y sus aserciones son las
vulnerabilidades sin corregir, que fallarán a partir de la Fase 1. Verificado
también que la app arranca y el catálogo carga.

### Fase 1 — Sesión y autenticación → cierra #1, #2, #4, #13, #16
- [ ] Reescribir `middleware/auth.go`: secreto desde config, `WithValidMethods(["HS256"])`,
      `iss`/`aud`, expiración corta, claim `jti`. **Borrar la constante `JWTSecret`.**
- [ ] Cookie `httpOnly` de sesión + cookie `dvbs_csrf` legible + header `X-CSRF-Token`.
- [ ] `internal/middleware/csrf.go` nuevo: exige el header en todo método ≠ GET/HEAD/OPTIONS,
      comparación en tiempo constante.
- [ ] `auth_service.go` → `Register` con DTO, ignora `role`.
- [ ] `auth_service.go` → reset con `crypto/rand` (32 bytes), **validar `expires_at`**,
      **single-use** (`used_at`), **revocar tokens previos**, y **`token_version`** en
      `users` para invalidar sesiones vivas.
- [ ] Respuesta **idéntica** en `forgot-password` exista o no el email.
- [ ] Nuevo paquete `internal/mailer/` con backends `SMTP` y `Log`. Eliminar el buzón HTTP.
- [ ] `middleware/admin.go` → consulta el rol en BD, no confía en el claim.
- [ ] Borrar `internal/middleware/cors_test.go` (afirma `*` explícitamente).

**Verificación:** `TestRegisterMassAssignmentRoleAdmin`, `TestForgedTokenGrantsAdminAccess`,
`TestSelfRegisteredAdminAccessesAdminPanel`, `TestAccountTakeoverChain`,
`TestExpiredTokenStillResets`, `TestTokenReusableAfterReset`, `TestOldJWTValidAfterReset`,
`TestForgotPasswordEnumeratesEmails` → en verde, ahora **afirmando el comportamiento seguro**.

### Fase 2 — Autorización y propiedad → cierra #3, #5, #9
- [ ] Queries con scope de propietario en `internal/store/queries/*.sql`; `sqlc generate`.
- [ ] Helper único `authorizeOwner` en `transport/book_handler.go` (donde ya viven
      `parseID`/`handleStoreError`). Aplicar a las 24 rutas afectadas.
- [ ] `router.go`: `POST/PUT/DELETE /books` → `Auth` + `AdminOnly`.
- [ ] `router.go`: `GET /users` y `GET /orders` → `AdminOnly`. El cliente usa `/users/me`.
- [ ] **Eliminar** `GET /payment-methods` (`router.go:66`) y `GET /addresses` (`router.go:73`).
- [ ] `user_service.go` → `UpdateUser` con DTO: solo `first_name`, `last_name`, `email`.
- [ ] `book_handler.go:32-48` → `writeError`/`handleStoreError` sin filtrar `err.Error()`.

**Verificación:** `TestIDORAnyTokenReadsAnyProfile`, `TestAnyTokenUpdatesAndDeletesAnyUser`,
`TestOrderIDORAnyTokenReadsAnyOrder`, `TestItemIDORAnyTokenReadsOthersItems`,
`TestAnyTokenEditsAndDeletesOthersReviews`, `TestAddressesIDORAndMultiPerUser` → invertidos.

### Fase 3 — Lógica de negocio y dinero → cierra #6, #7, #8, #10, #11, #14, #18
- [ ] `CheckoutRequest` → `{items: [{book_id, quantity}], coupon_code?}`. **Se descartan
      `total` y `user_id` del body.**
- [ ] Totales en **centavos enteros**, calculados desde el precio real del libro.
      `quantity` validada `1..10`.
- [ ] **Transacción** que envuelve: decremento de stock, orden, items, débito y cupón.
      `ROLLBACK` ante cualquier fallo.
- [ ] Débito atómico: `UPDATE users SET balance_cents = balance_cents - ? WHERE id = ?
      AND balance_cents >= ?`, validando `RowsAffected()`. Elimina el TOCTOU.
- [ ] `balance_cents` fuera del JSON del usuario y fuera de `UpdateUser`.
- [ ] **Borrar el `time.Sleep(75ms)`** de `coupon_service.go:41`.
- [ ] Tabla `coupon_redemptions` con `UNIQUE (coupon_id, user_id)`.
- [ ] Canje atómico: `UPDATE coupons SET used_count = used_count + 1 WHERE id = ?
      AND used_count < max_uses AND expires_at > CURRENT_TIMESTAMP`, validando `RowsAffected()`.
- [ ] **Eliminar `internal/service/discount_ledger.go`** (52 líneas de estado en memoria).
      El descuento pasa a ser campo de la orden.
- [ ] Reseñas: rating 1-5 en servicio **y** `CHECK`; autor del JWT; exigir orden pagada;
      `UNIQUE (book_id, user_id)`.
- [ ] Cupones: `POST`/`PUT`/`DELETE` → `AdminOnly`; validar rangos.
- [ ] **Propagar `context.Context`** en todos los stores.
- [ ] `internal/migration/000002_security_hardening.up.sql`: `coupon_redemptions`,
      `users.token_version`, `password_reset_tokens.used_at`, `DROP COLUMN cvv`, CHECKs,
      índices en `password_reset_tokens.token` y `books.isbn`.

**Verificación:** `TestCreateOrderAcceptsTamperedTotal`, `TestAddItemAcceptsTamperedUnitPrice`,
`TestAddItemInvalidQuantityAndNoStockDecrement`, `TestReviewWithoutPurchaseAccepted`,
`TestReviewAcceptsInvalidRatings`, `TestAnyAuthenticatedUserCreatesCoupons`,
`TestCouponsAcceptAbsurdValues`, `TestCouponRedeemRaceCompoundsDiscount`,
`TestCouponRaceConditionMaxUsesOne`, `TestCreditBlocksPurchaseUnlessDiscounted`,
`TestCheckoutIgnoresExpiredCoupon` → invertidos. Los dos de carrera deben afirmar
**exactamente 1** operación ganadora.

### Fase 4 — Exposición de datos → cierra #12 — **COMPLETADA**
- [x] `DROP COLUMN payment_methods.cvv` (migración `000003`). PCI-DSS prohíbe
      almacenar el CVV tras la autorización: no se cifra, **se elimina**.
- [x] `payment_methods` → `brand`, `last4`, `expiry_month`, `expiry_year`.
- [x] Tipo de respuesta con los campos sensibles en `json:"-"`. Nunca devolver el PAN.
- [x] `model.User` deja de exponer `role` y `balance_cents` en respuestas públicas.
- [x] `admin_service.go:Stats` → `SELECT COUNT(*)` y `SUM(total_cents)` en vez de
      cargar todas las filas de todas las tablas.

**Corrección colateral:** los agregados se añadieron a los ficheros `.sql` con una
escritura que dejó el cuerpo de `SumOrderTotals` huérfano **debajo** de la última
query, de modo que sqlc le asoció `SELECT count(*) FROM orders`. El panel pintaba
el número de órdenes con formato de dinero. `CountOrders` y `SumOrderTotals` están
fijados por `TestAdminStatsCountRevenueNotVolume`, que además comprueba que una orden
cancelada o reembolsada **sale** de la cifra de ingresos.

**Verificación:** `TestPaymentDataExposedInResponses`, `TestGlobalListingExposesAllCards`,
`TestAnyTokenReadsOthersCards` → invertidos y en verde.

### Fase 5 — Configuración → cierra #15 — **COMPLETADA**
- [x] `middleware/cors.go`: allowlist desde `APP_ORIGIN`, `Allow-Credentials: true`,
      `Vary: Origin`, y `OPTIONS` respondiendo solo si el origen está permitido
      (403 si no, que es más honesto que un 204 silencioso). Con cookies, `*` es
      además ilegal: los navegadores lo descartan.
- [x] `internal/middleware/ratelimit.go`: token bucket en memoria por IP sobre
      `/auth/*` y `POST /uploads`, con `RateLimit-*` y `Retry-After`.
- [x] `AUTH_RATE_PER_MINUTE`, `AUTH_RATE_BURST`, `UPLOAD_RATE_PER_MINUTE`,
      `UPLOAD_RATE_BURST` en `config.Load()`, con validación.

**Lo que hacía falta para que el limitador limitara de verdad** (los tres fallos
eran silenciosos, ninguno daba un error visible):
1. `X-Forwarded-For` **no lo enviaba nginx**: solo `X-Real-IP`. Ahora
   `$proxy_add_x_forwarded_for`, que añade la IP del cliente a la cadena en vez de
   reemplazar el valor entrante.
2. `SetTrustedProxies` solo aceptaba IPs literales, así que un bloque CIDR de
   Docker se descartaba con un warning. Ahora acepta `IP` y `CIDR`, que es lo
   único que se puede escribir sin conocer de antemano la IP del contenedor proxy.
3. `TRUSTED_PROXIES` estaba vacío, luego **todos los clientes compartían el cubo
   del proxy** y un solo usuario podía agotar el límite de la tienda entera.
   `docker-compose.yml` lo pone a `172.16.0.0/12`.
4. `cleanup()` no lo llamaba nadie. `StartJanitor` lo invoca en un ticker;
   `Cleanup` es público y devuelve cuántas entradas quitó.

**Límite conocido y declarado:** el estado está en memoria, así que es por proceso.
Corta el volumen, no un atacante distribuido. Y con `TRUSTED_PROXIES` mal
configurado el límite se vuelve inútil o se vuelve global; por eso hay tests para
las dos direcciones (`TestTrustedProxyAcceptsACIDRBlock`).

### Fase 6 — Uploads y XSS → cierra #17 — **COMPLETADA**
- [x] `upload_handler.go`: allowlist cerrada `jpeg/png/gif`; **re-encode** con
      `image/jpeg`/`image/png`/`image/gif` de la stdlib (destruye payloads
      embebidos); y **extensión generada por el servidor**, nunca la del cliente.
      Límites de bytes, de píxeles y de lado.
- [ ] **WebP fuera de la allowlist, contra lo que decía este plan.** La Allowlist
      original incluía `webp` con un encoder `nil`, y `nil` significaba *guardar
      los bytes del cliente*: la única comprobación eran dos bytes mágicos más
      `nosniff`, así que el fichero que el navegador acababa decodificando no lo
      había parseado nunca el proceso que lo guardó. `image/` no trae decodificador
      de WebP, y la solución no es una comprobación mejor: es **no aceptar el
      formato**. Todo lo que queda tiene re-encoder real, luego todo byte
      almacenado lo produjo este proceso. `TestEveryStoredImageWasReEncodedByThisProcess`
      lo comprueba para los tres formatos.
- [x] `Serve`: content-type desde allowlist **cerrada**, más `X-Content-Type-Options: nosniff`,
      `Content-Disposition: inline` y `Cache-Control: immutable`. `image/svg+xml`
      desaparece del sistema.
- [x] `BookDetail.jsx`: `<object data>` → `<img src>`.
- [x] `client.js`: `credentials: 'include'`, `X-CSRF-Token`, token fuera de
      `localStorage`, `AuthContext` con guarda de sesión caducada (401 → evento).
- [x] El `fetch` crudo del upload migrado al mismo cliente.
- [x] Comentarios `VULNERABLE (#…)` de `web/src/` eliminados.
- [x] `Cart.jsx` (`setCart` inexistente) y `Profile.jsx` (`href="#/login"`).
- [x] El cliente ya no envía `user_id` en reseñas ni en checkout.
- [x] CSP en `index.html` (meta) y en nginx (`web/security-headers.conf`), **medida
      sobre `dist/index.html` real**: el build de Vite 5 no emite `<script>` ni
      `<style>` en línea, así que **no hace falta `unsafe-inline`**. Google Fonts
      queda limitado a `fonts.googleapis.com` y `fonts.gstatic.com` en vez de caer
      en un comodín. El snippet se incluye en los tres `location` que añaden
      cabeceras propias, porque un `add_header` anula los del padre.
- [x] `RequireAuth` / `RequireAdmin` para `/profile` y `/admin`, más ruta 404.
- [x] Ajustado el hack de `bypass` de `vite.config.js` (solo queda `/admin`).

**Cosas de frontend que no estaban en el plan y aparecieron al reescribir:**
- El carrito guardaba `{id, price}` sin `quantity`; `undefined` en la suma daba
  `NaN` en pantalla. `lib/cart.js` normaliza al leer y al escribir, porque
  `localStorage` es la única entrada de esta aplicación que el usuario posee
  entero: se puede editar a mano, quedar de una versión anterior, o no ser un array.
- El carrito no guarda **ningún** precio. Todo total sale del catálogo que envió el
  servidor, así que un carrito abierto desde antes de un cambio de precio no ofrece
  pagar la cifra vieja.
- `addToCart` sube la cantidad de una línea existente en vez de apilar una segunda
  línea del mismo libro.
- El badge del topbar contaba líneas, no unidades: tres copias de un título
  marcaban "1".
- `Cart.jsx` hacía `setCatalog(res.data || [])` y luego `.find()`: un cuerpo de
  error es un objeto, y eso tiraba la página. Ahora solo un array es catálogo.
- `Profile.jsx` pedía `GET /orders`, que es admin-only (#5), así que el historial
  salía siempre vacío. Ahora usa `GET /orders/me` (§ Fase 7).

**Verificación:** `TestReviewImageUploadSVGStoredXSS` → `.svg` da 415 y el content-type
servido nunca es `image/svg+xml`.

### Fase 7 — Panel admin funcional — **COMPLETADA**
- [x] Backend: CRUD de libros, gestión de órdenes con cambio de estado, moderación de
      reseñas, gestión de cupones, cambio de rol. Todo bajo `Auth` + `AdminOnly` con
      verificación en BD.
- [x] Frontend: pestañas (Libros / Órdenes / Usuarios / Cupones / Reseñas). El panel
      vuelve a cargar tras cada cambio en lugar de actualizar su propio estado: un
      panel que se optimiza puede mentir sobre lo que se guardó.
- [x] Gate de rol con `AuthContext`; ninguna pestaña decide identidad. Ocultar una
      pestaña es cortesía y el servidor es el control.

**Lo que faltaba en el backend y se añadió:**
- `GET /admin/reviews`: cola de moderación. El `JOIN` con libros y autores está en
  la query, y es el único punto del store de reseñas que trae una dirección de
  correo, así que vive en un tipo aparte (`store.ModerationRow`) y no en
  `model.Review`, que es lo que sale en la página pública del libro.
- `PUT /admin/users/{id}/role`, con dos Negative Cases: rol desconocido → 400, y
  auto-degradación → 409. Es el único cambio que puede dejar al último admin fuera
  del panel y es un clic de distancia en un desplegable.
- `GET /orders/me` para el historial del cliente. `OrderService.ListForUser` ya
  existía sin ruta; el perfil pedía la ruta equivocada y por eso salía vacío.
- `BookRequest`: los libros se decodificaban de `model.Book` directo, así que `id`
  era un campo que el cliente podía escribir. Con `DisallowUnknownFields` el DTO
  convierte eso en un 400 en vez de un no-op silencioso.

**Lo que faltaba en el modelo de datos y se corrigió:**
- `PUT /orders/{id}` permitía que el **dueño** de la orden escribiera su propio
  estado, y `completed` es exactamente el estado en el que la regla de reseñas
  confía como prueba de compra. Poner un pedido en "completada" sin comprar era
  una petición. Ahora solo un admin mueve una orden, y el vocabulario es cerrado.
- `000006_order_status_check` normaliza los estados que ya existían y añade el
  `CHECK`. **El vocabulario es `pending, completed, shipped, delivered, cancelled,
  refunded`**: `completed` es lo que crea el checkout y en lo que se apoya la regla
  de reseñas. Se descartó `paid` a propósito — dos nombres para un estado son
  como un informe deja de contar sin que nadie lo note.

### Fase 8 — Tests de regresión
- [x] `TestMain` de `transport/test_main_test.go` ya no duplica las 55 rutas; usa
      `router.New(...)`.
- [x] Invertidos los tests de cupón (`coupon_redeem_race_test.go`), de reseñas
      (`review_coupon_handler_test.go`) y de administración. Criterio: **cada test
      que afirmaba un bug tiene una contraparte que afirma el comportamiento
      seguro**.
- [x] **Reset de contraseña atómico.** `AuthService.ResetPassword` consume el token,
      escribe el hash y sube `token_version` dentro de **una** transacción
      (`store.Runner.WithinTx`). Antes eran tres sentencias: el token se gastaba, la
      contraseña cambiaba, y si la tercera fallaba el usuario se quedaba sin poder
      reintentar **con la sesión del atacante todavía viva**. El hash se calcula
      *antes* de abrir la transacción: bcrypt es lento y mantener un lock de fila
      mientras se hashea serializaría todos los resets de la tienda.
      `TestResetIsAtomicUnderConcurrentRedemption` exige que de 8 canjes del mismo
      código gane **exactamente 1**.
- [x] **`GetAllPaymentMethods` borrado.** Era la consulta que devuelve *toda* la tabla
      de tarjetas. No le quedaba ninguna ruta ni ningún llamador, pero era la
      vulnerabilidad #12 en forma de código muerto esperando a que alguien cableara
      una ruta. El test que la usaba **afirmaba la capacidad** ("el listado global
      devuelve filas"), así que se sustituyó por una comprobación de que la tarjeta
      de un usuario no aparece en la lista de otro.
- [x] **Propagación de contextos.** Los `context.Background()` de
      `internal/store/` —56 en el punto de partida, contados con
      `git grep -c context.Background() HEAD -- 'internal/store/*.go'`— son 0.
      Cada método de store, de servicio y de handler recibe el
      `context.Context`; los handlers pasan `r.Context()`. Los tests usan
      `t.Context()`, y `cmd/seed` pasa un contexto raíz explícito desde `main()`.
      `TestStoreHonoursACancelledContext` y `TestStoreHonoursAnExpiredDeadline`
      fallan si algún store vuelve a ignorar el suyo.
- [x] `uniqueTestEmail` usaba `time.Now().UnixNano()%1000000`, que **colisiona**
      entre dos registros en la misma microsegundo: el segundo insertaba contra el
      índice único y el test fallaba con un 500 por clave duplicada que no tenía
      nada que ver con lo que estaba comprobando. Ahora es un contador más el pid.
- [x] **Esquema limpio por ejecución + fixtures.** Añadidos helpers
      `WithTestTransaction` y `WithTestServerInTransaction` en
      `internal/transport/test_main_test.go` que ejecutan el test dentro de una
      transacción que se hace rollback al final. Esto aísla los writes sin
      necesitar truncar tablas ni IDs únicos. Los tests existentes pueden
      migrarse gradualmente; los nuevos deben usar estos helpers.
- [x] **Punto de control antes de tocar la base de datos: suite completa en verde
      sobre PostgreSQL.** `go build ./...`, `go vet ./...` y `go test ./... -count=1`
      en verde, y `internal/transport -count=3` sin flakes.

**Tests añadidos al cerrar las fases 5-8** (cada uno falla si el arreglo se
revierte): `TestStoreHonoursACancelledContext`, `TestStoreHonoursAnExpiredDeadline`,
`TestAdminStatsCountRevenueNotVolume`, `TestAdminCanChangeARole`,
`TestOrderHistoryIsTheCallersOwn`, `TestOrderStatusIsRestrictedAndClosed`,
`TestStockAndOrderLineMoveTogether`, `TestEditingALineReturnsExactlyTheDifference`,
`TestResetIsAtomicUnderConcurrentRedemption`, `TestJanitorSweepsIdleBuckets`,
`TestTrustedProxyAcceptsACIDRBlock`, `TestWebPIsRefused`,
`TestEveryStoredImageWasReEncodedByThisProcess`.

**Tests añadidos al arreglar el panel (§3.9):**
`TestEveryIdentifierSurvivesJSON` y `TestBookIDIsQuotedOnTheWire` (ningún
identificador se entrecomilla de más, ninguno pierde precisión en un `float64`,
y el dinero sigue siendo número),
`TestDataServiceDeleteOfAMissingBookIsNotFound` (un borrado que no borra no se
reporta como borrado), y el existente
`TestDataServiceDeleteUsesDeleteWithTheIdAsText` corregido para que el fake
responda `row_affect: 1`, que es lo que devuelve el endpoint de verdad.

### Fase 9 — Migración a TiDB
- [x] Reescribir el DDL de una vez, consolidando las Fases 0-3, en
      `internal/migration/tidb/000001_init.up.sql` con `CREATE TABLE IF NOT EXISTS`.
      Descartar los ficheros de Postgres.
- [x] Nuevo `cmd/migrate` con `go:embed`, ejecutado al boot antes del seed
      (no hay hook `initdb` contra una base gestionada).
- [x] `sqlc.yaml`: `engine: "postgresql"` → `"mysql"`, `sql_package: "pgx/v5"` →
      `"database/sql"`. `sqlc generate`.
- [x] Reescribir los **10 métodos `Create*`** de `internal/store/*.go`:
      usando `sqlc :execresult` + `LastInsertId()` + `SELECT`. Generado automáticamente.
- [x] `internal/store/helpers.go` (43 líneas) eliminado: no hay tipos pgx que convertir.
- [x] Dependencias: entra `go-sql-driver/mysql`; sale todo `jackc/*` y `puddle`.
- [x] `config.go`: DSN `parseTime=true&charset=utf8mb4&tls=tidb`; `mysql.RegisterTLSConfig`
      con `ServerName` (derivado de `TIDB_HOST` o del DSN).
- [x] Pool: `SetMaxOpenConns(20)`, `SetMaxIdleConns(5)`,
      **`SetConnMaxLifetime(5 * time.Minute)`**, `SetConnMaxIdleTime(1 * time.Minute)`.
- [x] Propagar `context.Context` (el `database/sql` lo necesita para timeouts y cancelación).
- [x] `pgx.ErrNoRows` → `sql.ErrNoRows`.
- [x] `docker-compose.yml`: fuera el servicio `db`; `DATABASE_URL` por `.env`.
- [x] Nuevo `GET /healthz` que hace `Ping` a TiDB, sustituyendo al healthcheck de Postgres.
- [x] **Decisión del usuario:** re-seed con `cmd/seed` (Opción A). `cmd/migrate` idempotente.

**Verificación:** `go build ./...`, `go vet ./...`, `go test ./internal/store/... ./internal/config/...` en verde contra TiDB, seed funciona, server arranca, `/healthz` responde 200 ok.

---

### Fase 10 — TiDB Data Service para el panel admin
- [x] `internal/dataservice/`: cliente HTTPS con Basic Auth, sobre verificado, y
      `Row` que convierte texto y **falla** en vez de inventar ceros.
- [x] `dataservice.Config.Validate()`: `config.Load()` **falla al arrancar** si
      `BOOKS_BACKEND=dataservice` y falta la credencial, o si la URL no es https.
- [x] `store.DataServiceBookStore`: CRUD del catálogo por HTTP, stock y conteo por
      SQL. `TestStockNeverTravelsOverHTTP` y `TestCountBooksStaysOnSQL` lo fijan.
- [x] `store.IsDuplicate` / `store.IsForeignKey` cubren los dos transportes.
- [x] `handleStoreError` devuelve **409** con mensaje para ISBN repetido y para
      borrar un libro que está en un pedido; antes ambos eran 500.
- [x] `BOOKS_BACKEND=sql` por defecto: sin Data App desplegado, la tienda funciona.
- [x] `endpoints.txt` con `ORDER BY … LIMIT` acotado al tope real del endpoint,
      `;` en el `DELETE` y `NULLIF` en la portada. El `SELECT` final del `INSERT`
      que devolvía la fila creada está **eliminado**: no servía para nada (§3.7-8).
- [x] `dataservice.Exec` para leer `row_affect`. Sin él no hay forma de saber si
      un `INSERT` escribió algo, y `Query` tiraba el dato.
- [x] `CreateBook` son **dos llamadas**: insertar y leer por ISBN. El id es
      `AUTO_RANDOM`, no se conoce antes, y TiDB 8.5.3 no tiene `RETURNING`.
- [x] **Tests de `internal/config` ya son herméticos.** No lo eran: heredaban del
      shell, así que un `DB_MAX_CONNS` exportado rompía las aserciones de
      default y un `ADMIN_EMAIL` suelto rompía el test de emparejamiento. Ahora
      `setEnv` limpia las 26 variables que `Load()` lee antes de fijar las suyas.

**Verificado contra el Data App real:** los cinco endpoints desplegados y
enrutados; `GET /books` 200 con 200 filas ordenadas por id; `GET /book` 200 por id;
`PUT`/`POST`/`DELETE` 200 con `row_affect`; 1062 y 1451 traducidos a 409. El 403
`Permission denied to execute DQL/DDL-DML statement` que costó tres keys y un
re-enlace del data source está en §3.7-11.

---

## 6. Tabla de traducción del dialecto (Fase 9)

| PostgreSQL actual | TiDB / MySQL |
|---|---|
| `BIGSERIAL PRIMARY KEY` | `BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY` |
| `INT GENERATED BY DEFAULT AS IDENTITY` | `INT NOT NULL AUTO_INCREMENT` |
| `TIMESTAMPTZ` | `DATETIME(3)` — `TIMESTAMP` corta en 2038 |
| `NUMERIC(10,2)` | `DECIMAL(10,2)` |
| `TEXT` + `UNIQUE` | `VARCHAR(n)` — no se indexa `TEXT` sin prefijo. Afecta a `coupons.code` y `password_reset_tokens.token` |
| `now()` | `CURRENT_TIMESTAMP` |
| `$1`, `$2` | `?` |
| `INSERT ... RETURNING *` | **no existe** → `INSERT` + `SELECT` por `LastInsertId()` |
| `pgx.ErrNoRows` | `sql.ErrNoRows` |
| `pgtype.Text` / `Numeric` / `Timestamptz` | `sql.NullString` / `decimal.Decimal` / `time.Time` |
| `pgxpool` | `sql.DB` + `go-sql-driver/mysql` |
| `sslmode` | `tls=true` |

---

## 7. Comandos

```bash
# Build y análisis
go build ./... && go vet ./...

# Tests (contra la base configurada en DATABASE_URL)
go test ./... -count=1

# Regenerar la capa de datos
sqlc generate

# Levantar todo
docker compose up --build

# Frontend
cd web && npm install && npm run dev
```

Al migrar a TiDB, los tests apuntan a la nube: usar una base de desarrollo separada.

---

## 8. Orden de entrega

1. **Fase 0** — cimientos y limpieza.
2. **Fases 1-6** — seguridad, por capa. Cada fix con su test de regresión en el mismo commit.
3. **Fase 7** — panel admin.
4. **Fase 8** — suite de regresión completa en verde sobre PostgreSQL.
   **Este es el punto de control antes de tocar la base de datos.**
5. **Fase 9** — migración a TiDB, empezando por DDL + `sqlc generate`, subiendo capa por capa.

---

## 9. Progreso

Actualizar esta sección al cerrar cada fase.

- [x] Plan aprobado por el usuario. Decisiones bloqueadas registradas en §2.
- [x] Auditoría completa del repositorio (backend y frontend) con hallazgos en §3.
- [x] Compatibilidad de TiDB verificada contra la documentación oficial (§3.6 y §6).
- [x] Fase 0 — Cimientos y limpieza
- [x] Fase 1 — Sesión y autenticación
- [x] Fase 2 — Autorización y propiedad
- [x] Fase 3 — Lógica de negocio y dinero
- [x] Fase 4 — Exposición de datos
- [x] Fase 5 — Configuración
- [x] Fase 6 — Uploads y XSS
- [x] Fase 7 — Panel admin funcional
- [x] Fase 8 — Tests de regresión (queda el esquema limpio por ejecución)
- [x] Fase 9 — Migración a TiDB
- [x] Fase 10 — TiDB Data Service para el panel admin
- [x] **Spec del profesor**: cuatro roles (`admin`/`capturista`/`auditor`/`customer`)
      con `RequireRoles` (Catálogo: admin+capturista; Usuarios/Estado de cuenta:
      admin+capturista+auditor; solo `admin` rota a otros admins), `is_active`
      implementado, `PUT /admin/users/{id}/role` con auto-degradación → 409,
      `/users/me` (y el historial de órdenes del perfil), panel con resena
      moderación vía `GET /admin/reviews`, y las tres interfaces del panel —
      Catálogo con historial en `audit_log`, Usuarios, Respaldos (listar/crear/
      descargar, con path traversal cerrado). Migración `000002_panel` aplicada
      a la nube (borrado el dir duplicado `internal/migration/tidb/`).
- [x] **Suite de transporte completa en verde contra TiDB**, incluido
      `TestBalanceCannotGoNegative` — ya no es el rojo conocido: su causa raíz
      era el umbral `_2` del §3.10-1.

### Lo que sigue abierto

- **Tests de transporte.** La suite completa está en verde contra TiDB, pero la
  infraestructura usa `testDB` directamente para helpers de setup
  (`insertOrderDirect`, `setBalance`, etc.) sin envolverlos en
  `WithTestTransaction`. Eso rompe el aislamiento: los usuarios creados vía
  API (en la transacción del test) no son visibles para los `INSERT` directos
  contra `testDB`. Hoy no estalla porque cada test usa emails/ISBN únicos, pero
  es una bomba de relojería. La solución es mover todos los helpers de setup a
  la API o usar `WithTestTransaction` de forma consistente. **No afecta al código
  de producción** (seed, server, migrate funcionan).
- **`nginx -t` sin ejecutar.** El usuario actual no está en el grupo `docker` y
  `sudo` pide contraseña, así que la configuración sólo se ha verificado de forma
  estructural (llaves balanceadas, los tres `include` resueltos) y con
  `docker compose config`, que sí pasa. El primer `docker compose up` en una
  máquina con acceso al daemon es la prueba real.

### Deuda técnica conocida (no bloquea; anotar para más adelante)

- `statusRecorder` ya **sí** implementa `Flusher`/`Hijacker`/`Pusher`/`ReaderFrom`
  (cerrado en Fase 0).
- `gofmt`: cerrado. `gofmt -l .` no devuelve nada; en Fase 0 quedaban 41 ficheros
  sin formatear porque no se quería mezclar un reformateo masivo con el trabajo de
  seguridad, y para cuando se llegó a la Fase 8 los que seguían sin tocar ya no
  eran los que más se habían movido.
- `docs/PLAN.md` menciona un `HashRouter` en un encabezado; la app usa
  `BrowserRouter`. Corrección trivial pendiente.
- No hay ruta 404 en el backend. El frontend sí la tiene.
- Google Fonts sigue siendo un third party, pero la CSP lo limita a sus dos
  dominios en vez de dejarlo caer en un comodín. Auto-hospedarlo sigue siendo la
  opción si el proyecto lo necesita.
