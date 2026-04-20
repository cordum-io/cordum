# Releasing `cordum-sdk`

The worker session does **not** publish or tag releases. A human release
operator performs the final tag push after merge.

## 1. Update versioned files

1. Bump `src/cordum_sdk/__init__.py` `__version__`
2. Update `CHANGELOG.md`
3. Commit the release prep on `main`

## 2. Run the local verification gate

From `sdk/python/`:

```bash
python -m pytest -q
python -m mypy src
python -m ruff check src tests
python -m build
python -m twine check dist/*
```

All five commands must be green before tagging.

## 3. Push the release tag

```bash
git tag sdk-python-v0.1.0
git push origin sdk-python-v0.1.0
```

The GitHub Actions workflow `.github/workflows/sdk-python.yml` will:

1. download the built sdist/wheel artifact
2. exchange GitHub OIDC for a short-lived PyPI token
3. publish to PyPI as `cordum-sdk`

## 4. Verify the release

After the publish job completes:

```bash
pip index versions cordum-sdk
```

Confirm the new version appears, then verify the package page:

- https://pypi.org/project/cordum-sdk/

## Trusted Publisher setup

One-time PyPI setup for the upstream repository:

- project: `cordum-sdk`
- owner: `cordum-io`
- repository: `cordum`
- workflow: `sdk-python.yml`
- environment: `pypi`

Do **not** create or store a `PYPI_API_TOKEN`.
