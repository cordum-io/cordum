# CLI (cordumctl)

`cordumctl` is the local control-plane CLI.

## Build and install

```bash
make build SERVICE=cordumctl
export PATH="$PWD/bin:$PATH"
```

## Common commands

```bash
cordumctl up
cordumctl status
cordumctl pack list
cordumctl pack install ./examples/hello-pack
```

Most CLI calls require:

```bash
export CORDUM_API_KEY="$(openssl rand -hex 32)"
export CORDUM_TENANT_ID=default
```

See `docs/cordumctl.md` for the full reference.
