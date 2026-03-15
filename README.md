# Go-LLM-Gateway 🚀

[![CI Pipeline](https://github.com/fxwio/go-llm-gateway/actions/workflows/ci.yml/badge.svg)](https://github.com/fxwio/go-llm-gateway/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/fxwio/go-llm-gateway?style=flat-square&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg?style=flat-square)](LICENSE)
[![Docker Pulls](https://img.shields.io/badge/Docker-Ready-2496ED?style=flat-square&logo=docker&logoColor=white)](Dockerfile)

A high-performance, lightweight API gateway for Large Language Models (LLMs) built natively in Go. Designed for enterprise-grade throughput, robust rate limiting, and seamless Server-Sent Events (SSE) streaming.

## ✨ Core Features

- 🚀 High-performance gateway pipeline with streaming passthrough
- 🛡️ Bearer token authentication and request rate limiting
- 🔄 Multi-provider routing for OpenAI-compatible upstreams
- 📊 Prometheus metrics and health checks
- 🧱 Production-oriented config validation and startup fail-fast

## 🚀 Quick Start

### 1. Prepare configuration

```bash
cp config.example.yaml config.yaml
cp .env.example .env
```

Fill the real secrets in `.env`:

```dotenv
GATEWAY_VALID_TOKENS=sk-gateway-token-001,sk-gateway-token-002
OPENAI_API_KEY=sk-your-openai-key
ANTHROPIC_API_KEY=sk-ant-your-anthropic-key
SILICONFLOW_API_KEY=sk-your-siliconflow-key
METRICS_BEARER_TOKEN=replace-with-a-long-random-token
```

`config.yaml` keeps non-secret runtime settings only. Provider API keys and metrics bearer tokens must be injected through environment variables.

### 2. Run with Docker Compose

```bash
docker compose up -d --build
```

The container reads configuration from `GATEWAY_CONFIG_PATH=/etc/go-llm-gateway/config.yaml`, and the compose file mounts your local `config.yaml` read-only into that path.

### 3. Usage

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-gateway-token-001" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Explain microservices in one sentence."}],
    "stream": true
  }'
```

## 🔐 Production notes

- Do not commit `config.yaml` or `.env`.
- Do not place provider `api_key` values inside YAML.
- The image does not bake configuration into the container; inject it through bind mount, Secret, or ConfigMap.
- The server fails fast on invalid timeout, CIDR, port, token, and provider `base_url` settings.
- A panic recovery middleware is enabled so a single bad request will not crash the whole process.

## 📜 License

Distributed under the MIT License. See LICENSE for more information.
