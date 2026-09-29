# DVBS — Librería

DVBS es una tienda de libros en línea: catálogo, carrito, checkout con cupones,
reseñas verificadas y un panel de administración para gestionar el negocio.

El proyecto nació como una aplicación de laboratorio con vulnerabilidades
deliberadas. Eso ya no es así: todas las fallas conocidas están corregidas y el
repositorio contiene código pensado para desplegarse, no para atacarse.

## Funcionalidad

- API REST en Go (`net/http`) sobre TiDB Cloud (compatible MySQL)
- Catálogo con búsqueda, categorías y filtros de orden
- Carrito y checkout con cupones de descuento
- Reseñas verificadas: una por libro y usuario, solo tras una compra pagada
- Registro e inicio de sesión con sesión por cookie `httpOnly` + token CSRF
- Perfil de cliente: pedidos, métodos de pago (tokenizados) y direcciones
- Recuperación de contraseña por correo, con token de un solo uso y caducidad
- Panel de administración: libros, pedidos, usuarios, cupones y reseñas
- Frontend en React servido por nginx detrás de un proxy inverso

## Requisitos

- Go 1.25 o superior
- TiDB Cloud (Starter tier) o MySQL 8.0+
- Node.js 20 o superior (solo para el frontend)
- Docker con Compose, si prefieres levantar todo en contenedores

## Configuración

La aplicación no tiene valores por defecto inseguros: si falta una variable
obligatoria, `config.Load()` falla y el proceso no arranca. Copia este bloque a un
archivo `.env` y ajústalo.

```bash
# Obligatorias
DATABASE_URL=mysql://user:pass@gateway01.us-east-1.prod.aws.tidbcloud.com:4000/dvbs?tls=tidb&parseTime=true&charset=utf8mb4
JWT_SECRET=            # openssl rand -base64 48   (mínimo 32 caracteres)

# Servidor
PORT=8000
UPLOAD_DIR=uploads

# Frontend: origen único permitido por CORS. Sin barra final.
APP_ORIGIN=http://localhost:3000

# Sesión
SESSION_TTL_MINUTES=15
COOKIE_SECURE=false    # true en producción; APP_ENV=production lo exige
RESET_TOKEN_TTL_MINUTES=30   # 1..1440

# Base de datos y contraseñas
DB_MAX_CONNS=20
BCRYPT_COST=12

# Administrador inicial. Se crea solo la primera vez; después el cambio de
# contraseña se hace desde el panel, no por variable de entorno.
ADMIN_EMAIL=admin@dvbs.test
ADMIN_PASSWORD=         # obligatoria si se define ADMIN_EMAIL

# Correo. Si SMTP_HOST y SMTP_FROM están vacíos, los mensajes se escriben en el
# log del servidor en vez de enviarse.
SMTP_HOST=
SMTP_PORT=587          # 465 para TLS implícito
SMTP_USER=
SMTP_PASSWORD=
SMTP_FROM=
```

Sobre `COOKIE_SECURE`: la cookie de sesión lleva `Secure` y `SameSite`. Con
`COOKIE_SECURE=false` la cookie viaja por http en claro, lo cual es aceptable solo
en desarrollo local. Si `APP_ENV=production` y la cookie no es segura, la
aplicación se niega a arrancar.

## Sesión, CSRF y recuperación de contraseña

La sesión viaja en una cookie `dvbs_session` `httpOnly`: el frontend nunca ve el
token, así que un XSS no puede robarlo. Como una cookie sí se envía sola, toda
petición que cambie estado debe además repetir un token CSRF en la cabecera
`X-CSRF-Token`. El cliente lo obtiene de `GET /auth/csrf` y lo lee de la cookie
`dvbs_csrf`, que es la única cookie que el JavaScript puede leer a propósito.

Las peticiones con cabecera `Authorization: Bearer` están exentas del CSRF: un
token que el navegador no guarda no puede ser montado por otra web. Es lo que
mantiene utilizables `curl` y los tests.

El token de recuperación son 32 bytes de `crypto/rand` en base64url, no un código
corto, y caduca según `RESET_TOKEN_TTL_MINUTES`. Solo se usa una vez, pedir uno
nuevo revoca el anterior, y completar el cambio invalida las sesiones vivas del
usuario.

## Puesta en marcha

### Con Docker

```bash
docker compose up --build
```

| Servicio | URL | Propósito |
|----------|-----|-----------|
| Tienda | http://localhost:3000 | Interfaz web |
| API REST | http://localhost:8000 | API directa, para `curl` o scripts |

El esquema se aplica al arrancar la API (ver `cmd/migrate`). La base de datos
es TiDB Cloud externo; no hay contenedor de base de datos local.

`docker-compose.yml` pasa la configuración al servicio de la API. Edita el
bloque `environment:` de `api` o usa un fichero `.env` junto al compose.

### Sin Docker

```bash
# 1. Esquema
go run ./cmd/migrate

# 2. Datos iniciales: 200 libros de catálogo ficticio, cupón DVBS20 y el admin
go run ./cmd/seed

# 3. API en el puerto 8000
go run ./cmd/server

# 4. Frontend en el puerto 5173, con proxy hacia la API
cd web && npm install && npm run dev
```

`cmd/seed` es idempotente: se puede repetir sin duplicar libros ni cupones. El
administrador se crea únicamente si no existe, y su contraseña no se vuelve a
imprimir ni a sobrescribir en ejecuciones posteriores.

## Pruebas

Las pruebas de integración corren contra una base de datos real, configurada en
`DATABASE_URL`.

```bash
go test ./... -count=1
```

Las pruebas crean filas reales y usan helpers transaccionales para aislar los
writes. Apuntan a una base de desarrollo; no las ejecutes contra datos que te
importen.

## Regenerar la capa de datos

La capa de persistencia se genera con [sqlc](https://sqlc.dev). Tras editar
`internal/store/queries/` o las migraciones:

```bash
sqlc generate
```

La configuración está en `sqlc.yaml` (engine: mysql) y la salida se escribe en
`internal/store/db/`.

## Estructura del proyecto

```
├── cmd/
│   ├── server/        # Punto de entrada de la API
│   ├── seed/          # Seed idempotente (libros, cupón, administrador)
│   └── migrate/       # Aplica migraciones SQL embebidas
├── internal/
│   ├── config/        # Configuración por entorno y pool de conexiones
│   ├── mailer/        # Envío de correo (backend SMTP o log)
│   ├── middleware/    # Autenticación, puerta de admin, CSRF, CORS, logging, recovery
│   ├── migration/     # Esquema SQL (fuente de verdad para sqlc)
│   ├── model/         # Estructuras de dominio con etiquetas JSON
│   ├── router/        # Registro de rutas y cadena de middleware
│   ├── service/       # Lógica de negocio
│   ├── store/         # Repositorios sobre el código generado por sqlc
│   └── transport/     # Handlers HTTP
├── web/               # Frontend React (Vite + nginx)
├── Dockerfile         # Build multi-etapa de la API en Go
├── docker-compose.yml
└── sqlc.yaml
```

## Notas de seguridad

Estas son las decisiones que sostienen el resto del diseño:

- **El precio lo calcula el servidor.** El cliente envía identificadores y
  cantidades; nunca importes. Todos los importes se manejan en centavos enteros.
- **Propiedad verificada en cada ruta.** Ningún handler acepta un `{id}` de la
  ruta sin comprobar que el recurso pertenece al sujeto de la sesión. Un recurso
  ajeno y un recurso inexistente devuelven ambos 404, para no filtrar existencia.
- **Los errores de base de datos no llegan al cliente.** Se registran en el
  servidor; la respuesta es un mensaje genérico.
- **Nada de secretos en el código.** JWT, SMTP y base de datos vienen del
  entorno, y la aplicación se niega a arrancar si faltan.
- **Los datos de tarjeta no se almacenan en claro.** Se conserva marca y los
  últimos cuatro dígitos. El CVV no se guarda: PCI-DSS prohíbe almacenarlo tras
  la autorización.
- **Las subidas se re-decodifican.** El servidor comprueba los bytes reales de la
  imagen, la vuelve a codificar y decide la extensión y el content-type. Un
  archivo que no sea una imagen válida se rechaza.

`AGENTS.md` documenta la arquitectura, las invariantes y el plan de migración a
TiDB Cloud.