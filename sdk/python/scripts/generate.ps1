$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RootDir = Split-Path -Parent $ScriptDir
$SpecPath = Join-Path $RootDir "..\..\docs\api\openapi\cordum-api.yaml"
$ConfigPath = Join-Path $RootDir "openapi-python-client.config.yaml"
$OutputPath = Join-Path $RootDir "src\cordum_sdk\_generated"

$LocalGenerator = Join-Path $RootDir ".venv\Scripts\openapi-python-client.exe"
$LocalPython = Join-Path $RootDir ".venv\Scripts\python.exe"

# openapi-python-client orders some imports by iterating a set, so fix the hash seed.
# The config's post-hook runs the first `ruff` on PATH: use the venv's pinned one.
$SavedSeed = $env:PYTHONHASHSEED
$SavedPath = $env:PATH
$env:PYTHONHASHSEED = "0"
$env:PATH = (Join-Path $RootDir ".venv\Scripts") + [IO.Path]::PathSeparator + $env:PATH
try {
  if (Test-Path $LocalGenerator) {
    & $LocalGenerator generate `
      --meta none `
      --path $SpecPath `
      --config $ConfigPath `
      --overwrite `
      --fail-on-warning `
      --output-path $OutputPath
  } elseif (Test-Path $LocalPython) {
    & $LocalPython -m openapi_python_client generate `
      --meta none `
      --path $SpecPath `
      --config $ConfigPath `
      --overwrite `
      --fail-on-warning `
      --output-path $OutputPath
  } elseif (Get-Command openapi-python-client -ErrorAction SilentlyContinue) {
    & openapi-python-client generate `
      --meta none `
      --path $SpecPath `
      --config $ConfigPath `
      --overwrite `
      --fail-on-warning `
      --output-path $OutputPath
  } else {
    & python -m openapi_python_client generate `
      --meta none `
      --path $SpecPath `
      --config $ConfigPath `
      --overwrite `
      --fail-on-warning `
      --output-path $OutputPath
  }
  exit $LASTEXITCODE
} finally {
  $env:PYTHONHASHSEED = $SavedSeed
  $env:PATH = $SavedPath
}
