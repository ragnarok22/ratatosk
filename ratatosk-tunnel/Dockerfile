ARG BUILD_FROM=ghcr.io/home-assistant/amd64-base:3.21
FROM ${BUILD_FROM}

ARG BUILD_ARCH
ARG RATATOSK_VERSION
ARG RATATOSK_SHA256_AMD64
ARG RATATOSK_SHA256_ARM64
ARG RATATOSK_SHA256_ARM

RUN apk add --no-cache ca-certificates bash curl

RUN set -eux; \
    case "${BUILD_ARCH}" in \
      amd64)   ARCH="amd64"; SHA256="${RATATOSK_SHA256_AMD64}" ;; \
      aarch64) ARCH="arm64"; SHA256="${RATATOSK_SHA256_ARM64}" ;; \
      armv7)   ARCH="arm"; SHA256="${RATATOSK_SHA256_ARM}" ;; \
      *)       echo "Unsupported architecture: ${BUILD_ARCH}" && exit 1 ;; \
    esac; \
    curl -fsSL -o /usr/local/bin/ratatosk \
      "https://github.com/ragnarok22/ratatosk/releases/download/${RATATOSK_VERSION}/ratatosk-cli-linux-${ARCH}"; \
    echo "${SHA256}  /usr/local/bin/ratatosk" | sha256sum -c -; \
    chmod +x /usr/local/bin/ratatosk

COPY run.sh /
RUN chmod +x /run.sh

CMD ["/run.sh"]
