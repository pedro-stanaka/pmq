FROM golang:1.27.4-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /pmq ./cmd/pmq

FROM alpine:3.23
RUN addgroup -S pmq && adduser -S -G pmq pmq && mkdir /data && chown pmq:pmq /data
USER pmq
COPY --from=build /pmq /usr/local/bin/pmq
ENV PMQ_LISTEN=0.0.0.0:8080 PMQ_DATA_DIR=/data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 CMD wget -q -O /dev/null http://127.0.0.1:8080/_pmq/health || exit 1
ENTRYPOINT ["pmq"]
CMD ["serve"]
