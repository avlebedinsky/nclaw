ARG WHISPER_CPP_VERSION=v1.9.4
ARG WHISPER_MODEL=small-q5_1
ARG WHISPER_MODEL_SHA256=ae85e4a935d7a567bd102fe55afc16bb595bdb618e11b2fc7591bc08120411bb
ARG WHISPER_MODELS_REV=5359861c739e955e79d9a303bcbc70fb988958b1

FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG BUILD_NUMBER=
ARG DOCKER_TAG=
RUN go build -ldflags "\
    -X github.com/nickalie/nclaw/internal/version.Version=${VERSION} \
    -X github.com/nickalie/nclaw/internal/version.Commit=${COMMIT} \
    -X github.com/nickalie/nclaw/internal/version.BuildDate=${BUILD_DATE} \
    -X github.com/nickalie/nclaw/internal/version.BuildNumber=${BUILD_NUMBER} \
    -X github.com/nickalie/nclaw/internal/version.DockerTag=${DOCKER_TAG}" \
    -o nclaw ./cmd/nclaw

FROM golang:1.25 AS builder-glibc

RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev && rm -rf /var/lib/apt/lists/*
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG BUILD_NUMBER=
ARG DOCKER_TAG=
RUN go build -ldflags "\
    -X github.com/nickalie/nclaw/internal/version.Version=${VERSION} \
    -X github.com/nickalie/nclaw/internal/version.Commit=${COMMIT} \
    -X github.com/nickalie/nclaw/internal/version.BuildDate=${BUILD_DATE} \
    -X github.com/nickalie/nclaw/internal/version.BuildNumber=${BUILD_NUMBER} \
    -X github.com/nickalie/nclaw/internal/version.DockerTag=${DOCKER_TAG}" \
    -o nclaw ./cmd/nclaw

FROM node:24-alpine AS whisper-builder
ARG WHISPER_CPP_VERSION
RUN apk add --no-cache build-base cmake git
RUN git clone --depth 1 --branch "${WHISPER_CPP_VERSION}" https://github.com/ggml-org/whisper.cpp /src
WORKDIR /src
RUN cmake -B build -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DGGML_NATIVE=OFF \
        -DWHISPER_BUILD_TESTS=OFF -DWHISPER_BUILD_SERVER=OFF && \
    cmake --build build --target whisper-cli -j"$(nproc)" && \
    install -m 0755 build/bin/whisper-cli /usr/local/bin/whisper-cli

FROM node:24-alpine AS whisper-model
ARG WHISPER_MODEL
ARG WHISPER_MODEL_SHA256
ARG WHISPER_MODELS_REV
RUN apk add --no-cache curl && mkdir -p /models && \
    curl -fsSL -o "/models/ggml-${WHISPER_MODEL}.bin" \
      "https://huggingface.co/ggerganov/whisper.cpp/resolve/${WHISPER_MODELS_REV}/ggml-${WHISPER_MODEL}.bin" && \
    echo "${WHISPER_MODEL_SHA256}  /models/ggml-${WHISPER_MODEL}.bin" | sha256sum -c -

# ---------- shared base ----------
FROM node:24-alpine AS base
ARG WHISPER_MODEL

LABEL org.opencontainers.image.source="https://github.com/nickalie/nclaw"

RUN apk add --no-cache \
    git \
    bash \
    curl \
    openssh-client \
    github-cli \
    chromium \
    harfbuzz \
    nss \
    freetype \
    ttf-freefont \
    tzdata \
    font-noto-emoji \
    gcompat \
    python3 \
    py3-pip \
    pipx \
    tini \
    ffmpeg \
    libgomp

ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
ENV AGENT_BROWSER_EXECUTABLE_PATH=/usr/bin/chromium-browser
ENV IS_SANDBOX=1
ENV CLAUDE_CONFIG_DIR=/root/.claude

COPY --from=whisper-builder /usr/local/bin/whisper-cli /usr/local/bin/whisper-cli
COPY --from=whisper-model /models/ /opt/whisper/
ENV NCLAW_WHISPER_MODEL=/opt/whisper/ggml-${WHISPER_MODEL}.bin

# Install uv (provides uv and uvx)
RUN curl -LsSf https://astral.sh/uv/install.sh | bash

# Install agent-browser (uses system Chromium via env vars above)
RUN npm install -g agent-browser

# Install skills
RUN npx -y skills add https://github.com/vercel-labs/skills --skill find-skills -g -y && \
    npx -y skills add https://github.com/anthropics/skills --skill skill-creator -g -y && \
    npx -y skills add https://github.com/vercel-labs/agent-browser --skill agent-browser -g -y && \
    mkdir -p /opt/nclaw-skills && cp -rL /root/.agents/skills/* /opt/nclaw-skills/

# Install Go (copy from builder)
COPY --from=builder /usr/local/go /usr/local/go
ENV PATH="/root/.local/bin:/usr/local/go/bin:${PATH}"

# Copy application binary
COPY --from=builder /build/nclaw /usr/local/bin/nclaw

COPY .claude/skills/schedule /opt/nclaw-skills/schedule
COPY .claude/skills/send-file /opt/nclaw-skills/send-file
COPY .claude/skills/webhook /opt/nclaw-skills/webhook

WORKDIR /app
ENTRYPOINT ["tini", "--", "nclaw"]

# ---------- claude ----------
FROM base AS claude
RUN curl -fsSL https://claude.ai/install.sh | bash
RUN set -eux; \
    case "$(apk --print-arch)" in \
        x86_64) arch=amd64 ;; \
        aarch64) arch=arm64 ;; \
        *) echo "unsupported fya architecture" >&2; exit 1 ;; \
    esac; \
    tag="$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/umputun/fya/releases/latest)"; \
    tag="${tag##*/}"; \
    curl -fsSL "https://github.com/umputun/fya/releases/download/${tag}/fya_${tag#v}_linux_${arch}.tar.gz" -o /tmp/fya.tar.gz; \
    tar -xzf /tmp/fya.tar.gz -C /usr/local/bin fya; \
    chmod +x /usr/local/bin/fya; \
    rm /tmp/fya.tar.gz
RUN echo '{"autoCompact": true}' > /root/.claude/settings.json

# ---------- multi-model ----------
FROM claude AS multi-model
RUN npm install -g claudish

# ---------- codex ----------
FROM base AS codex
RUN npm install -g @openai/codex

# ---------- copilot ----------
FROM node:24-slim AS copilot-base

RUN apt-get update && apt-get install -y --no-install-recommends \
    git bash curl openssh-client ca-certificates \
    gh python3 python3-pip pipx tzdata tini \
    && rm -rf /var/lib/apt/lists/*

ENV IS_SANDBOX=1
ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1

# Install uv
RUN curl -LsSf https://astral.sh/uv/install.sh | bash

COPY --from=builder-glibc /usr/local/go /usr/local/go
COPY --from=builder-glibc /build/nclaw /usr/local/bin/nclaw
ENV PATH="/root/.local/bin:/usr/local/go/bin:${PATH}"
ENV NCLAW_DATA_DIR=/app/data
ENV CLAUDE_CONFIG_DIR=/root/.claude
WORKDIR /app
ENTRYPOINT ["tini", "--", "nclaw"]

FROM copilot-base AS copilot
ARG BUILD_DATE=unknown
RUN echo "copilot install at ${BUILD_DATE}" && npm install -g @github/copilot@latest
COPY .claude/skills/schedule /opt/nclaw-skills/schedule
COPY .claude/skills/send-file /opt/nclaw-skills/send-file
COPY .claude/skills/webhook /opt/nclaw-skills/webhook
RUN mkdir -p /root/.claude/skills /root/.copilot && \
    ln -s /root/.claude/skills /root/.copilot/skills

# ---------- gemini ----------
FROM base AS gemini
RUN npm install -g @google/gemini-cli

# ---------- all-in-one (default) ----------
FROM claude AS all
RUN npm install -g claudish
RUN npm install -g @openai/codex
RUN npm install -g @google/gemini-cli
