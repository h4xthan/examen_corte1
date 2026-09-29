FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/server ./cmd/server && \
    CGO_ENABLED=0 go build -o /bin/seed ./cmd/seed && \
    CGO_ENABLED=0 go build -o /bin/migrate ./cmd/migrate

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /bin/server /bin/server
COPY --from=build /bin/seed /bin/seed
COPY --from=build /bin/migrate /bin/migrate
EXPOSE 8000
# migrate es idempotente (CREATE TABLE IF NOT EXISTS + skip de duplicados):
# sobre el esquema ya aplicado no hace nada y sobre una base recién creada deja
# el esquema listo para el seed. seed también es idempotente (ignora ISBNs
# existentes), así que este entrypoint puede correr en cada deploy sin destruir
# datos.
ENTRYPOINT ["/bin/sh", "-c", "/bin/migrate && /bin/seed && exec /bin/server"]