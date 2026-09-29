FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/server ./cmd/server && \
    CGO_ENABLED=0 go build -o /bin/seed ./cmd/seed

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /bin/server /bin/server
COPY --from=build /bin/seed /bin/seed
EXPOSE 8000
ENTRYPOINT ["/bin/sh", "-c", "/bin/seed && exec /bin/server"]