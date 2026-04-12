ARG BUILD_FROM
FROM ${BUILD_FROM}

ARG BUILD_ARCH

RUN apk add --no-cache ca-certificates bash curl

RUN set -eux; \
    case "${BUILD_ARCH}" in \
      amd64)   ARCH="amd64" ;; \
      aarch64) ARCH="arm64" ;; \
      armv7)   ARCH="arm" ;; \
      *)       echo "Unsupported architecture: ${BUILD_ARCH}" && exit 1 ;; \
    esac; \
    curl -fsSL -o /usr/local/bin/ratatosk \
      "https://github.com/ragnarok22/ratatosk/releases/latest/download/ratatosk-cli-linux-${ARCH}"; \
    chmod +x /usr/local/bin/ratatosk

COPY run.sh /
RUN chmod +x /run.sh

CMD ["/run.sh"]
