FROM ghcr.io/foundry-rs/foundry:v1.4.3

USER root
RUN if command -v apk >/dev/null 2>&1; then \
      apk add --no-cache jq bash python3 py3-tomli; \
    elif command -v apt-get >/dev/null 2>&1; then \
      apt-get update && apt-get install -y --no-install-recommends jq bash python3 python3-tomli && rm -rf /var/lib/apt/lists/*; \
    fi
USER foundry
