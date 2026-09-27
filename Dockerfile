FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/zonenan-server ./cmd/server
RUN CGO_ENABLED=0 go build -trimpath -o /out/zonenan-migrate ./cmd/migrate
RUN CGO_ENABLED=0 go build -trimpath -o /out/zonenan-worker ./cmd/worker
RUN CGO_ENABLED=0 go build -trimpath -o /out/zonenan-preflight ./cmd/preflight
RUN CGO_ENABLED=0 go build -trimpath -o /out/zonenan-backfill ./cmd/backfill
RUN CGO_ENABLED=0 go build -trimpath -o /out/zonenan-postflight ./cmd/postflight

FROM alpine:3.21
RUN apk add --no-cache tzdata \
    && addgroup -S zonenan \
    && adduser -S -G zonenan zonenan
WORKDIR /app
COPY --from=build /out/zonenan-server /usr/local/bin/zonenan-server
COPY --from=build /out/zonenan-migrate /usr/local/bin/zonenan-migrate
COPY --from=build /out/zonenan-worker /usr/local/bin/zonenan-worker
COPY --from=build /out/zonenan-preflight /usr/local/bin/zonenan-preflight
COPY --from=build /out/zonenan-backfill /usr/local/bin/zonenan-backfill
COPY --from=build /out/zonenan-postflight /usr/local/bin/zonenan-postflight
USER zonenan
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/zonenan-server"]
