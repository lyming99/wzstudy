FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && \
    addgroup -S -g 10001 wzstudy && \
    adduser -S -D -H -u 10001 -G wzstudy wzstudy && \
    mkdir -p /var/lib/wzstudy && \
    chown wzstudy:wzstudy /var/lib/wzstudy && \
    chmod 0700 /var/lib/wzstudy
ENV DATA_DIR=/var/lib/wzstudy
VOLUME ["/var/lib/wzstudy"]
WORKDIR /app
COPY --from=build /out/ /app/
USER wzstudy
EXPOSE 8080
CMD ["/app/api"]
