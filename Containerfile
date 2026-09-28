FROM golang:1.27.1-bookworm@sha256:69a7b9788769bec032d238959b61854e9ae87f57be9029ec04e9885fabf99195 AS go-toolchain
WORKDIR /src
COPY go.mod go.sum ./
COPY internal/contracts ./internal/contracts
COPY internal/fault ./internal/fault
COPY internal/humaninput ./internal/humaninput
COPY internal/toolgateway ./internal/toolgateway
COPY cmd/turnyard-tool-gateway ./cmd/turnyard-tool-gateway
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -o /turnyard-tool-gateway ./cmd/turnyard-tool-gateway
COPY cmd/turnyard-tool-bridge ./cmd/turnyard-tool-bridge
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -o /turnyard-tool-bridge ./cmd/turnyard-tool-bridge
FROM node:26-bookworm-slim@sha256:662933cf47f013bc8e4beb31a6116448427a82057ba7c42c97e4c5ba766504c2
COPY --from=go-toolchain /usr/local/go /usr/local/go
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates python3 && rm -rf /var/lib/apt/lists/* \
    && npm install -g opencode-ai@1.18.32 @moonshot-ai/kimi-code@2.1.1 @anthropic-ai/claude-code@2.1.283 @openai/codex@0.157.1 \
    && case "$(uname -m)" in \
         aarch64) npm install -g '@openai/codex-linux-arm64@npm:@openai/codex@0.157.1-linux-arm64' ;; \
         x86_64) npm install -g '@openai/codex-linux-x64@npm:@openai/codex@0.157.1-linux-x64' ;; \
         *) echo 'unsupported agent image architecture' >&2; exit 1 ;; \
       esac
COPY --from=go-toolchain /turnyard-tool-bridge /usr/local/bin/turnyard-tool-bridge
COPY --from=go-toolchain /turnyard-tool-gateway /usr/local/bin/turnyard-tool-gateway
RUN useradd -m -u 10001 agent && mkdir -p /workspace /state && chown agent:agent /workspace /state
USER agent
ENV PATH=/usr/local/go/bin:$PATH
ENV HOME=/state/home XDG_CONFIG_HOME=/state/config XDG_DATA_HOME=/state/data XDG_CACHE_HOME=/state/cache
WORKDIR /workspace
ENTRYPOINT ["opencode"]
