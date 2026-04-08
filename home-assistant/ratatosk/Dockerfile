ARG BUILD_FROM
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /ratatosk-cli ./cmd/cli

FROM ${BUILD_FROM}

RUN apk add --no-cache ca-certificates bash
COPY --from=builder /ratatosk-cli /usr/local/bin/ratatosk
COPY home-assistant/ratatosk/run.sh /

RUN chmod +x /run.sh

CMD ["/run.sh"]
