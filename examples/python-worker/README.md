# Hello Worker (Python)

Python worker example for `job.hello-pack.echo`.

## Run

```bash
python -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt

export NATS_URL=${NATS_URL:-nats://localhost:4222}
python worker.py
```

This example expects the Cordum Python SDK (`cordum` package).
