FROM golang:1.27-alpine AS build

ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

WORKDIR /src/backend

COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/shop-mall-server ./cmd/server

FROM alpine:3.22

RUN addgroup -S app \
    && adduser -S -G app app \
    && mkdir -p /app/migrations /var/lib/shop-mall/images \
    && chown -R app:app /app /var/lib/shop-mall

WORKDIR /app
COPY --from=build --chown=app:app /out/shop-mall-server /app/shop-mall-server
COPY --from=build --chown=app:app /src/backend/migrations /app/migrations

USER app
EXPOSE 8080
ENTRYPOINT ["/app/shop-mall-server"]
