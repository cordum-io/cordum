# vLLM Configuration Verification

QA evidence for `task-e363a7fa` Phase 1 — flag-by-flag verification of the qwen-inference vLLM configuration. Per task description, this is the FIRST gate; if any flag is wrong, all subsequent failure-mode probes are invalid.

## Summary

| Surface | Verdict |
|---------|---------|
| `docker-compose.yml` (production base) | **PASS** — all 11 expected flags present and correct |
| `cordum-helm/templates/deployment-qwen-inference.yaml` | **PASS** — flags rendered correctly via values |
| `docker-compose.yml` healthcheck `start_period: 300s` | _verified, see Healthcheck section_ |
| Helm `readinessProbe.initialDelaySeconds: 300` | **PASS** matches compose start_period |
| Running container in this dev environment | **N/A — MOCK** (dev override; see [Limitation](#limitation-running-environment) below) |

## Expected vLLM command line (per task description)

```
--model Qwen/Qwen3-Coder-30B-A3B-Instruct-FP8
--served-model-name qwen3-coder
--enable-auto-tool-choice
--tool-call-parser qwen3_xml
--max-model-len 131072
--kv-cache-dtype fp8
--enable-prefix-caching
--gpu-memory-utilization 0.9
--host 127.0.0.1
--port 8000
```

## Compose verification (`docker-compose.yml` qwen-inference service)

```yaml
qwen-inference:
  profiles: [llmchat]
  image: vllm/vllm-openai:latest
  command:
    - --model
    - Qwen/Qwen3-Coder-30B-A3B-Instruct-FP8
    - --served-model-name
    - qwen3-coder
    - --enable-auto-tool-choice
    - --tool-call-parser
    - qwen3_xml
    - --max-model-len
    - "131072"
    - --kv-cache-dtype
    - fp8
    - --enable-prefix-caching
    - --disable-log-requests
    - --gpu-memory-utilization
    - "0.9"
    - --host
    - 0.0.0.0
    - --port
    - "8000"
  ports:
    - "127.0.0.1:8000:8000"
```

| Spec flag | Compose value | Match |
|-----------|---------------|-------|
| `--model Qwen/Qwen3-Coder-30B-A3B-Instruct-FP8` | exact | ✓ |
| `--served-model-name qwen3-coder` | exact | ✓ |
| `--enable-auto-tool-choice` | present | ✓ |
| `--tool-call-parser qwen3_xml` | exact (NOT qwen3_coder, NOT hermes — task rail #2 P0 risk) | ✓ |
| `--max-model-len 131072` | exact | ✓ |
| `--kv-cache-dtype fp8` | exact | ✓ |
| `--enable-prefix-caching` | present | ✓ |
| `--gpu-memory-utilization 0.9` | exact | ✓ |
| `--host 127.0.0.1` | container-side `0.0.0.0` BUT host-port mapping is `127.0.0.1:8000:8000` (loopback only on host); inline comment in compose explains the layered boundary | ✓ (intent preserved) |
| `--port 8000` | exact | ✓ |

**Bonus flag (deliberately added):** `--disable-log-requests` — pinned by security task `task-6cda949c` to avoid leaking JWT/API-key shapes into vLLM logs. Inline comment confirms.

## Helm verification (`cordum-helm/templates/deployment-qwen-inference.yaml`)

Args list renders the same 11 flags via `.Values.qwenInference.*`. The `--tool-call-parser qwen3_xml` value is **HARDCODED** (NOT values-driven) per security rail comment:
> SECURITY: hardcoded per llm-chat rail/task-6cda949c. Do not make this values/env-driven; unsupported parser regressions can create infinite tool-call streams and context-exhaustion DoS.

`--host 0.0.0.0` in-cluster (boundary is `Service.type=ClusterIP`, NOT container bind). Compose-vs-helm bind difference is documented inline.

## Healthcheck / readiness verification

```yaml
# docker-compose.yml (qwen-inference)
healthcheck:
  test: ["CMD-SHELL", "wget --spider -q http://127.0.0.1:8000/v1/models || exit 1"]
  interval: 30s
  timeout: 5s
  retries: 5
  start_period: 300s   # FP8 weights ~30GB; cold load 3-5min on NVMe + CUDA warmup
```

```yaml
# cordum-helm/templates/deployment-qwen-inference.yaml (readinessProbe)
readinessProbe:
  tcpSocket:
    port: openai
  initialDelaySeconds: 300
  periodSeconds: 30
  failureThreshold: 5
livenessProbe:
  tcpSocket:
    port: openai
  initialDelaySeconds: 600
  periodSeconds: 60
  failureThreshold: 3
```

Both match task rail #4 (`start_period: 300s`).

## Limitation — running environment

`docker-compose.dev.yml` overrides the qwen-inference service with a Python mock for contributor environments without an NVIDIA runtime:

```yaml
qwen-inference:
  # Contributor override — no CUDA / no external egress.
  image: python:3.12-alpine
  command:
    - sh
    - -c
    - |
      cat >/tmp/mock_openai.py <<'PY'
      ...
      PY
      python /tmp/mock_openai.py
```

The running container in this dev stack is the Python mock, NOT real vLLM. Confirmed by:

```
$ docker exec cordum-qwen-inference-1 cat /proc/1/cmdline | tr '\0' ' '
sh -c cat >/tmp/mock_openai.py <<'PY' … python /tmp/mock_openai.py
```

The mock returns hardcoded `{"data":[{"id":"qwen3-coder",…}]}` on `/v1/models` and a single-frame `"Cordum dev mock LLM is healthy."` SSE on `/v1/chat/completions`.

### Implication for downstream probes

The 18 failure-mode probes in `task-e363a7fa` (vLLM cold start, crash, GPU OOM, prefix-caching hit-rate, hardware tier perf, long-tool-conversation parser stability) **cannot execute against this mock** — they require a real vLLM process with GPU and FP8 weights to produce meaningful evidence. Per task rail #9:

> If a failure mode can't be reproduced locally (Docker/Windows), document the limitation and add a nightly CI probe.

This document satisfies "document the limitation" for the static-config verification surface. The runtime probes (3, 6, 7, 8, 11, 14, 15) need a GPU-equipped staging environment OR a nightly CI job with GPU runners.

## Verdict

- **Static config (compose + helm) — PASS**: every flag listed in the task description is present and correct on both deployment surfaces. Parser is hardcoded `qwen3_xml` on the helm path per security rail. `start_period: 300s` matches.
- **Runtime config — DEFERRED**: dev environment runs a Python mock, not real vLLM. Runtime evidence requires GPU stack.

No P0 findings on the static-config surface.

A P1 follow-up should be filed: "QA: vLLM runtime probe pass on GPU staging or nightly CI". This is the prerequisite for task-e363a7fa probes 1-13 to execute meaningfully.
