# syntax=docker/dockerfile:1.7

# Etapa de build: compila um binário estático, sem CGO.
# Imagem fixada por tag e digest (multi-arquitetura).
FROM golang:1.27.4-alpine3.23@sha256:f70850ef6ce341e8b6427f677049145d63c0b7ce002d4d7e3d464828b1b96dcd AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/payments ./cmd/payments

# Agendador: crond do busybox chamando `payments reconcile --daily`.
# Fica antes da etapa final para que ela continue sendo o alvo padrão.
FROM alpine:3.23.0 AS scheduler
COPY --from=build /out/payments /usr/local/bin/payments
COPY deploy/scheduler/entrypoint.sh /usr/local/bin/scheduler-entrypoint
ENTRYPOINT ["/usr/local/bin/scheduler-entrypoint"]

# Etapa final: distroless sem shell, usuário não-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/payments /usr/local/bin/payments
USER nonroot:nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/usr/local/bin/payments"]
CMD ["api"]
