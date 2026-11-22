FROM golang:1.22-alpine AS builder

WORKDIR /src
COPY go.mod ./
COPY pkg/ ./pkg/
COPY cmd/ ./cmd/

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bin/ethify-relay ./cmd/ethify-relay

FROM alpine:3.20

RUN apk add --no-cache iptables iproute2 bash
COPY --from=builder /bin/ethify-relay /usr/local/bin/ethify-relay

ENV ETHIFY_LISTEN=":51820" \
    ETHIFY_TUN="ethify0" \
    ETHIFY_SUBNET="10.88.0.0/24" \
    ETHIFY_MTU="1400"

EXPOSE 51820/udp

ENTRYPOINT ["/usr/local/bin/ethify-relay"]
