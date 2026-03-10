# Go-LLM-Gateway 🚀

[![Go Version](https://img.shields.io/github/go-mod/go-version/fxwio/go-llm-gateway?style=flat-square&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg?style=flat-square)](LICENSE)
[![Docker Pulls](https://img.shields.io/badge/Docker-Ready-2496ED?style=flat-square&logo=docker&logoColor=white)](Dockerfile)

A high-performance, lightweight API gateway for Large Language Models (LLMs) built natively in Go. Designed for enterprise-grade throughput, robust rate limiting, and seamless Server-Sent Events (SSE) streaming.

## 💡 Why This Project?

Integrating multiple LLM providers (OpenAI, Anthropic, Gemini) directly into client applications often leads to fragmented authentication, difficult cost tracking, and lack of traffic control. 

Drawing on years of experience in building high-concurrency enterprise microservices, **Go-LLM-Gateway** acts as a unified, secure, and highly optimized central proxy. It standardizes traffic into the OpenAI API format while providing essential observability and protection.

## ✨ Core Features

- 🚀 **Zero-Allocation Routing:** Optimized HTTP request multiplexing and dynamic provider routing.
- 🌊 **Native SSE Support:** Perfect streaming (`stream: true`) pass-through without buffering destruction.
- 🛡️ **Enterprise Security:** In-memory Token-Bucket rate limiting and Bearer token authentication.
- 🔄 **Multi-Provider Support:** Unified interface for OpenAI, Anthropic (Claude), and any OpenAI-compatible endpoints (e.g., Ollama, vLLM).
- 📊 **Observability:** High-performance structured logging powered by Uber's `zap`.

## 🏗 Architecture



The gateway utilizes a strict middleware onion model, ensuring traffic is validated and throttled before ever establishing a connection to the downstream AI providers.

## 🚀 Quick Start

### 1. Configuration
Create a `config.yaml` in the root directory:

```yaml
server:
  port: 8080
auth:
  valid_tokens:
    - "sk-my-gateway-token-001"
  rate_limit_qps: 10
  rate_limit_burst: 20
providers:
  - name: "openai"
    base_url: "[https://api.openai.com](https://api.openai.com)"
    api_key: "sk-your-openai-key"
    models:
      - "gpt-4o"
      - "gpt-3.5-turbo"
```

### 2. Run with Docker
Bash
docker build -t go-llm-gateway .
docker run -p 8080:8080 -v $(pwd)/config.yaml:/app/config.yaml go-llm-gateway
3. Usage
Use any standard OpenAI SDK or curl to interact with the gateway:

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-my-gateway-token-001" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Explain microservices in one sentence."}],
    "stream": true
  }'
```

### 3. Usage
Use any standard OpenAI SDK or curl to interact with the gateway:

```Bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-my-gateway-token-001" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Explain microservices in one sentence."}],
    "stream": true
  }'
```

### 📜 License
Distributed under the MIT License. See LICENSE for more information.
