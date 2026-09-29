# DVBS — Arquitectura y plan de despliegue

> Este documento describe la arquitectura actual. Sustituye al plan de la v1 del
> laboratorio, que describía un `HashRouter` y una aplicación deliberadamente
> vulnerable: nada de eso describe el sistema hoy.

`AGENTS.md` es la fuente de verdad operativa: contiene el plan de trabajo por
fases, el mapeo de vulnerabilidades a correcciones y las invariantes de
seguridad. Este documento explica **cómo está montado el sistema**, no qué falta
por hacer.

## 1. Arquitectura general

```
┌──────────────────┐      ┌───────────────────┐      ┌──────────────────────┐
│       web        │      │        api        │      │          db          │
│  React 18+Vite   │─────▶│   Go (net/http)   │─────▶│      TiDB Cloud      │
│  servido por     │proxy │   :8000           │      │   Starter (free)     │
│  nginx :3000     │      └───────────────────┘      └──────────────────────┘
└──────────────────┘
```

- **Navegador**: abre `http://localhost:3000`.
- **API**: accesible en `http://localhost:8000` para `curl` o scripts.
- **Health check**: `GET /healthz` → `200 ok` si la base responde.
- **Un comando**: `docker compose up --build`.

### Puertos

| Servicio | Puerto host | Puerto interno | Notas |
|---|---|---|---|
| web (nginx) | **3000** | 80 | Interfaz |
| api (Go) | **8000** | 8000 | API REST |
| db | externo | 4000 | TiDB Cloud gestionado; no hay contenedor local |

## 2. Capas del backend

El flujo de una petición va siempre en el mismo sentido:

```
router → middleware → transport (JSON) → service (reglas) → store (SQL) → db
```

| Capa | Responsabilidad | No debe hacer |
|---|---|---|
| `router` | Registrar rutas y componer el middleware | Lógica de negocio |
| `middleware` | Autenticación, CSRF, admin, CORS, rate limit, logging, recovery | Tocar el dominio |
| `transport` | Decodificar JSON, validar forma, responder | Confiar en el precio o el propietario sin preguntar al service |
| `service` | Reglas de negocio, propiedad, dinero, transacciones | SQL escrito a mano |
| `store` | Repositorios sobre sqlc, un `context.Context` por llamada | `context.Background()` |

Dos reglas que la estructura hace cumplir por convención:

- El dinero se maneja **solo en centavos enteros**. `float64` no aparece en
  ningún importe.
- Ningún handler acepta un `{id}` de la ruta sin que el service verifique la
  propiedad del recurso.

## 3. Sesión y autenticación

La sesión viaja en una cookie `httpOnly` (`dvbs_session`), no en `localStorage`.
Un token CSRF legible por JavaScript (`dvbs_csrf`) acompaña cada petición que
modifica estado, y el servidor lo compara en tiempo constante.

- La cookie lleva `Secure` en producción y `SameSite` para bloquear el envío
  entre sitios.
- El rol se comprueba contra la fila real de `users` en cada petición
  administrativa, no contra un claim del token.
- Cambiar la contraseña invalida las sesiones vivas del usuario (bump de `token_version`).

## 4. Datos y dinero

- `orders.total` y `users.balance_cents` se guardan en **centavos enteros**.
- El checkout recalcula el total desde el precio real del libro; el cliente solo
  envía identificadores y cantidades (1..10).
- El débito es una actualización condicional atómica
  (`balance_cents >= ?`), no una lectura seguida de una escritura, para eliminar
  la condición de carrera.
- El canje de cupones y el decremento de stock ocurren dentro de una transacción
  que revierte completa ante cualquier fallo.

## 5. Migración de base de datos (TiDB Cloud)

La capa de datos se genera con `sqlc` a partir de las migraciones en
`internal/migration/tidb/`. El destino final es **TiDB Cloud Starter**, que es
MySQL-compatible pero con diferencias que afectan a la capa de persistencia.

El plan de migración, sus trampas confirmadas y la tabla de traducción del
dialecto están en `AGENTS.md` §3.6 y §6. Puntos clave ya aplicados:

- `INSERT ... RETURNING` no existe en TiDB → los métodos `Create*` usan
  `sqlc :execresult` + `LastInsertId()` + `SELECT`.
- Pool con `ConnMaxLifetime=5min` y `ConnMaxIdleTime=1min` porque TiDB Cloud
  cierra conexiones ociosas a ~340s.
- `AUTO_RANDOM` para PKs (IDs grandes no secuenciales, mejor distribución).
- `DATETIME(3)` en vez de `TIMESTAMP` (corta en 2038).
- Índice parcial de Postgres → columna generated `live_user_id` + `UNIQUE` en
  `password_reset_tokens`.
- `sqlc.yaml`: `engine: "mysql"`, `sql_package: "database/sql"`.
- Driver: `go-sql-driver/mysql` + TLS `mysql.RegisterTLSConfig("tidb", ...)`.

## 6. Despliegue

- `Dockerfile`: build multi-etapa de la API en Go, imagen final mínima.
- `web/nginx.conf`: sirve el build de Vite, hace proxy de `/api` hacia el
  contenedor de la API y fija `client_max_body_size` acorde al límite del
  endpoint de subida.
- `docker-compose.yml`: `api` + `web` en desarrollo. No hay servicio `db` local.
- `cmd/migrate`: aplica migraciones embebidas (`go:embed`) idempotentemente.

Todas las variables de entorno están documentadas en el `README.md`. La
aplicación se niega a arrancar si falta una obligatoria.