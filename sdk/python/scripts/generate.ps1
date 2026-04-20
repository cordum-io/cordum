$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RootDir = Split-Path -Parent $ScriptDir
$SpecPath = Join-Path $RootDir "..\..\docs\api\openapi\cordum-api.yaml"
$ConfigPath = Join-Path $RootDir "openapi-python-client.config.yaml"
$OutputPath = Join-Path $RootDir "src\cordum_sdk\_generated"

$LocalGenerator = Join-Path $RootDir ".venv\Scripts\openapi-python-client.exe"
$LocalPython = Join-Path $RootDir ".venv\Scripts\python.exe"

if (Test-Path $LocalGenerator) {
  & $LocalGenerator generate `
    --meta none `
    --path $SpecPath `
    --config $ConfigPath `
    --overwrite `
    --output-path $OutputPath
} elseif (Test-Path $LocalPython) {
  & $LocalPython -m openapi_python_client generate `
    --meta none `
    --path $SpecPath `
    --config $ConfigPath `
    --overwrite `
    --output-path $OutputPath
} elseif (Get-Command openapi-python-client -ErrorAction SilentlyContinue) {
  & openapi-python-client generate `
    --meta none `
    --path $SpecPath `
    --config $ConfigPath `
    --overwrite `
    --output-path $OutputPath
} else {
  & python -m openapi_python_client generate `
    --meta none `
    --path $SpecPath `
    --config $ConfigPath `
    --overwrite `
    --output-path $OutputPath
}
