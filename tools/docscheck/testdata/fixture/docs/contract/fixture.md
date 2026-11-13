# Fixture contract

<!-- docscheck: env -->
| Variable | Default | Required | Description |
|----------|---------|----------|-------------|
| `FIX_VAR` | `b` | No | wrong default |
| `FIX_OK` | `x` | No | right default |
| `FIX_NONE` | — | No | no default |
| `FIX_PROSE` | `1` (dev), `2` (prod) | No | prose default |
| `FIX_PLAIN` | `y` | No | read with os.Getenv only |
| `FIX_TWO` | `p` | No | two code defaults |

Read one thing with `GET /api/v1/things/{id}`.

The old `GET /api/v1/missing` call is gone.

<!-- docscheck: command -->
```bash
cordumctl status
```

<!-- docscheck: skip reason="needs a live cluster to run" -->
```bash
kubectl apply -f deploy.yaml
```
