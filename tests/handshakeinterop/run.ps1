[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$CapRoot
)

$ErrorActionPreference = 'Stop'
$CordumRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$CapRoot = (Resolve-Path $CapRoot).Path
if ([string]::IsNullOrWhiteSpace($env:CAP_HANDSHAKE_REDIS_URL)) {
    throw 'CAP_HANDSHAKE_REDIS_URL is required'
}

function Assert-NativeSuccess {
    param([Parameter(Mandatory = $true)][string]$Action)
    if ($LASTEXITCODE -ne 0) {
        throw "$Action failed with exit code $LASTEXITCODE"
    }
}

function Assert-CapRevision {
    $CapSha = (& git -C $CapRoot rev-parse HEAD).Trim()
    Assert-NativeSuccess 'read full CAP revision'
    $CapVersion = (& go -C $CordumRoot list -m -f '{{.Version}}' github.com/cordum-io/cap/v2).Trim()
    Assert-NativeSuccess 'read Cordum CAP pin'
    if ($CapVersion -match '-([0-9a-f]{12})$') {
        # Pseudo-version: the commit hash is embedded in the version string.
        $PinnedSha = (& git -C $CapRoot rev-parse "$($Matches[1])^{commit}").Trim()
        Assert-NativeSuccess 'resolve Cordum CAP revision in source repository'
    }
    else {
        # Clean release tag (e.g. v2.16.1): resolve the tag itself in the
        # checked-out CAP source (requires the checkout step to have fetched
        # tags) instead of parsing a hash out of the version string.
        $PinnedSha = (& git -C $CapRoot rev-parse "refs/tags/$CapVersion^{commit}" 2>$null).Trim()
        if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($PinnedSha)) {
            throw "Cordum CAP version $CapVersion has no matching tag in the checked-out CAP source"
        }
    }
    if ($CapSha -ne $PinnedSha) {
        throw "CAP source $CapSha does not match Cordum pin $PinnedSha"
    }
    if (-not [string]::IsNullOrWhiteSpace($env:CAP_HANDSHAKE_CAP_SHA) -and
        $CapSha -ne $env:CAP_HANDSHAKE_CAP_SHA.Trim()) {
        throw "CAP source $CapSha does not match CAP_HANDSHAKE_CAP_SHA"
    }
}

function Invoke-InDirectory {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][scriptblock]$Command
    )
    Push-Location $Path
    try {
        & $Command
    }
    finally {
        Pop-Location
    }
}

Assert-CapRevision
$TempBase = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
$TempRoot = Join-Path $TempBase ('cap-handshake-interop-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $TempRoot | Out-Null

function Export-TrackedDirectory {
    param(
        [Parameter(Mandatory = $true)][string[]]$GitPath,
        [Parameter(Mandatory = $true)][string]$Name
    )
    $Archive = Join-Path $TempRoot "$Name.zip"
    $Tree = Join-Path $TempRoot "$Name-source"
    & git -C $CapRoot archive --format=zip --output=$Archive HEAD @GitPath
    Assert-NativeSuccess "archive tracked $($GitPath -join ',')"
    Expand-Archive -LiteralPath $Archive -DestinationPath $Tree
    return $Tree
}

function Build-GoClient {
    $Consumer = Join-Path $TempRoot 'go-consumer'
    $Proxy = Join-Path $TempRoot 'go-proxy'
    $Sha = (& git -C $CapRoot rev-parse --short=12 HEAD).Trim()
    Assert-NativeSuccess 'read CAP revision'
    $Version = "v2.5.3-handshake.0.g$Sha"
    & python (Join-Path $CapRoot 'tools/onboarding/build_go_proxy.py') `
        --root $CapRoot --output $Proxy --version $Version
    Assert-NativeSuccess 'build local Go module proxy'
    New-Item -ItemType Directory -Path (Join-Path $Consumer 'client'), (Join-Path $Consumer '.gomodcache') | Out-Null
    Copy-Item (Join-Path $CapRoot 'test/interop/handshake/go_client.go') (Join-Path $Consumer 'client')
    Copy-Item (Join-Path $CapRoot 'test/interop/handshake/go_client_mutations.go') (Join-Path $Consumer 'client')
    $ProxyUrl = ([Uri]([IO.Path]::GetFullPath($Proxy))).AbsoluteUri
    $Previous = @($env:GOWORK, $env:GOMODCACHE, $env:GOPROXY, $env:GONOSUMDB, $env:GOSUMDB)
    try {
        Invoke-InDirectory $Consumer {
            & go mod init example.com/cap-handshake-interop
            Assert-NativeSuccess 'initialize Go consumer'
            $env:GOWORK = 'off'
            $env:GOMODCACHE = Join-Path $Consumer '.gomodcache'
			$env:GOPROXY = $ProxyUrl
			$env:GOSUMDB = 'off'
			& go mod download "github.com/cordum-io/cap/v2@$Version"
			Assert-NativeSuccess 'download CAP Go module from local proxy'
			& go mod edit "-require=github.com/cordum-io/cap/v2@$Version"
			Assert-NativeSuccess 'pin CAP Go module'
			$env:GOPROXY = "$ProxyUrl,https://proxy.golang.org,direct"
			$env:GONOSUMDB = 'github.com/cordum-io/cap/v2'
			$env:GOSUMDB = $Previous[4]
            & go mod tidy
            Assert-NativeSuccess 'resolve Go consumer'
            & go mod vendor
            Assert-NativeSuccess 'vendor Go consumer'
            $env:GOPROXY = 'off'
            & go build -mod=vendor -o (Join-Path $TempRoot 'go-client.exe') ./client
            Assert-NativeSuccess 'build installed Go client'
        }
    }
    finally {
        $env:GOWORK, $env:GOMODCACHE, $env:GOPROXY, $env:GONOSUMDB, $env:GOSUMDB = $Previous
    }
    $env:CAP_HANDSHAKE_GO_CLIENT = Join-Path $TempRoot 'go-client.exe'
}

function Build-PythonClient {
    $Builder = Join-Path $TempRoot 'python-builder'
    $Dist = Join-Path $TempRoot 'python-dist'
    $Venv = Join-Path $TempRoot 'python-venv'
    $SourceTree = Export-TrackedDirectory -GitPath 'sdk/python' -Name 'python'
    $Source = Join-Path $SourceTree 'sdk/python'
    New-Item -ItemType Directory -Path $Dist | Out-Null
    & python -m venv $Builder
    Assert-NativeSuccess 'create Python build environment'
    $BuilderPython = Join-Path $Builder 'Scripts/python.exe'
    & $BuilderPython -m pip install --disable-pip-version-check 'build==1.2.2.post1'
    Assert-NativeSuccess 'install pinned Python build frontend'
    & $BuilderPython -m build --outdir $Dist $Source
    Assert-NativeSuccess 'build Python wheel'
    $Wheels = @(Get-ChildItem -LiteralPath $Dist -Filter '*.whl')
    if ($Wheels.Count -ne 1) { throw "expected one Python wheel, found $($Wheels.Count)" }
    & python -m venv $Venv
    Assert-NativeSuccess 'create Python consumer environment'
    $ConsumerPython = Join-Path $Venv 'Scripts/python.exe'
    & $ConsumerPython -m pip install --disable-pip-version-check --no-cache-dir $Wheels[0].FullName
    Assert-NativeSuccess 'install Python wheel'
    & $ConsumerPython -m pip check
    Assert-NativeSuccess 'check Python wheel dependencies'
    $Client = Join-Path $TempRoot 'python_client.py'
    Copy-Item (Join-Path $CapRoot 'test/interop/handshake/python_client.py') $Client
    $env:CAP_HANDSHAKE_PYTHON_BIN = $ConsumerPython
    $env:CAP_HANDSHAKE_PYTHON_CLIENT = $Client
}

function Build-NodeClient {
    $Artifacts = Join-Path $TempRoot 'node-artifacts'
    $Consumer = Join-Path $TempRoot 'node-consumer'
    $SourceTree = Export-TrackedDirectory -GitPath @('sdk/node', 'proto') -Name 'node'
    $Source = Join-Path $SourceTree 'sdk/node'
    New-Item -ItemType Directory -Path $Artifacts, $Consumer | Out-Null
    Invoke-InDirectory $Source {
        & npm.cmd ci
        Assert-NativeSuccess 'install Node SDK dependencies'
        & npm.cmd run build
        Assert-NativeSuccess 'build Node SDK'
        & npm.cmd pack --pack-destination $Artifacts
        Assert-NativeSuccess 'pack Node SDK'
    }
    $Packages = @(Get-ChildItem -LiteralPath $Artifacts -Filter '*.tgz')
    if ($Packages.Count -ne 1) { throw "expected one Node package, found $($Packages.Count)" }
    Invoke-InDirectory $Consumer {
        & npm.cmd init -y | Out-Null
        Assert-NativeSuccess 'initialize Node consumer'
        & npm.cmd install --ignore-scripts --no-audit --no-fund $Packages[0].FullName `
            'typescript@5.9.3' '@types/node@18.19.130'
        Assert-NativeSuccess 'install packed Node SDK'
        Copy-Item (Join-Path $CapRoot 'test/interop/handshake/node_client.ts') $Consumer
        $TsConfig = '{"compilerOptions":{"target":"ES2022","module":"CommonJS","moduleResolution":"Node","strict":true,"esModuleInterop":true,"skipLibCheck":true,"outDir":"dist"},"include":["node_client.ts"]}'
        [IO.File]::WriteAllText((Join-Path $Consumer 'tsconfig.json'), $TsConfig, [Text.UTF8Encoding]::new($false))
        & (Join-Path $Consumer 'node_modules/.bin/tsc.cmd') --project tsconfig.json
        Assert-NativeSuccess 'compile installed Node client'
    }
    $env:CAP_HANDSHAKE_NODE_BIN = (Get-Command node).Source
    $env:CAP_HANDSHAKE_NODE_CLIENT = Join-Path $Consumer 'dist/node_client.js'
}

function Remove-OwnedTemp {
    $ResolvedTemp = [IO.Path]::GetFullPath($TempRoot)
    $Leaf = Split-Path $ResolvedTemp -Leaf
    if (-not $ResolvedTemp.StartsWith($TempBase, [StringComparison]::OrdinalIgnoreCase) -or
        -not $Leaf.StartsWith('cap-handshake-interop-', [StringComparison]::Ordinal)) {
        throw "refusing unsafe cleanup path: $ResolvedTemp"
    }
    Remove-Item -LiteralPath $ResolvedTemp -Recurse -Force
}

try {
    Build-GoClient
    Build-PythonClient
    Build-NodeClient
    Invoke-InDirectory $CordumRoot {
        & go test -v -tags=handshakeinterop -count=1 -timeout=4m ./tests/handshakeinterop
        Assert-NativeSuccess 'run handshake interop gate'
    }
}
finally {
    Remove-OwnedTemp
}
