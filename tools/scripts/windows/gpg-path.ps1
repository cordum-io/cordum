# gpg-path.ps1 — shared GPG-path adapter for install.ps1 + install_test.ps1.
#
# Git for Windows / MSYS2 / Cygwin ship a POSIX gpg that treats `C:\...`
# (and even `C:/...`) as repo-relative because the drive letter is a
# valid POSIX path component. The result is the EDGE-151 reopen #2 bug:
# `--homedir`, `--import`, `--output`, and `--verify` arguments end up
# prepended with the gpg CWD (`/d/Cordum/cordum/C:\Users\...`) and fail
# to open.
#
# Gpg4Win / native Windows gpg accepts native paths just fine.
#
# This helper centralises the detection + conversion so install.ps1 and
# install_test.ps1 stay PowerShell-native on the cmdlet side and pass
# converted paths only to gpg arguments. PowerShell `Get-ChildItem`,
# `Set-Content`, `Test-Path`, `Remove-Item`, etc. keep using `-LiteralPath`
# with absolute native Windows paths.
#
# Exposed functions:
#   Resolve-CordumGpgCommand  -- locate gpg by -GpgExe / PATH (throws if missing)
#   Get-CordumGpgPathMode      -- 'posix' for MSYS/Cygwin gpg, 'native' otherwise
#   ConvertTo-CordumGpgArgPath -- normalise a single filesystem argument

#Requires -Version 5.1

Set-StrictMode -Version 2.0

function Resolve-CordumGpgCommand {
    [CmdletBinding()]
    param(
        [string]$GpgExe = 'gpg.exe'
    )
    $cmd = Get-Command $GpgExe -ErrorAction SilentlyContinue
    if (-not $cmd -and $GpgExe -ne 'gpg') {
        $cmd = Get-Command 'gpg' -ErrorAction SilentlyContinue
    }
    if (-not $cmd) {
        throw "Resolve-CordumGpgCommand: gpg not found (requested: $GpgExe)"
    }
    return $cmd
}

function Get-CordumGpgPathMode {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][System.Management.Automation.CommandInfo]$GpgCommand
    )
    # First check the exe path — Git\usr\bin gpg is the dominant MSYS
    # surface on Windows endpoints (Git for Windows).
    $sourceLower = $GpgCommand.Source.ToLowerInvariant()
    if ($sourceLower -match '\\git\\usr\\bin\\gpg' -or
        $sourceLower -match '/git/usr/bin/gpg' -or
        $sourceLower -match '\\msys64\\' -or
        $sourceLower -match '/msys64/' -or
        $sourceLower -match '\\cygwin\\' -or
        $sourceLower -match '/cygwin/') {
        return 'posix'
    }
    # Fall back to the version banner so non-canonical install locations
    # of MSYS/Cygwin gpg still report 'posix' (e.g. user-installed MSYS2
    # outside the default prefix).
    try {
        $ver = (& $GpgCommand.Source --version 2>&1 | Out-String)
        if ($ver -match '(?im)\b(msys|cygwin|mingw)\b') {
            return 'posix'
        }
    } catch {
        # If gpg --version itself fails, fall through to native; the
        # caller will get the real failure when they invoke gpg.
    }
    return 'native'
}

function ConvertTo-CordumGpgArgPath {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Path,
        [Parameter(Mandatory = $true)][ValidateSet('posix', 'native')][string]$Mode
    )
    if ([string]::IsNullOrEmpty($Path)) {
        throw "ConvertTo-CordumGpgArgPath: Path argument must be non-empty"
    }
    # Use GetFullPath, NOT Resolve-Path: Resolve-Path errors when the
    # target does not yet exist (e.g. `--output SHA256SUMS.asc` where the
    # signature file is the gpg output and has not been created yet).
    $absolute = [System.IO.Path]::GetFullPath($Path)
    if ($Mode -eq 'native') {
        return $absolute
    }
    # POSIX mode: use cygpath -u to convert C:\Users\... to /c/Users/...
    # which MSYS / Cygwin gpg can open. We MUST shell out to cygpath
    # rather than munging the string ourselves because cygpath knows the
    # mount table (e.g. /cygdrive prefix on stock Cygwin, /-prefix on
    # MSYS, custom mounts from /etc/fstab).
    $cygpath = Get-Command 'cygpath.exe' -ErrorAction SilentlyContinue
    if (-not $cygpath) {
        $cygpath = Get-Command 'cygpath' -ErrorAction SilentlyContinue
    }
    if (-not $cygpath) {
        throw "ConvertTo-CordumGpgArgPath: POSIX gpg requires cygpath but it was not found on PATH; install Git for Windows or MSYS2/Cygwin, or pass -GpgExe pointing at Gpg4Win/native gpg"
    }
    $converted = & $cygpath.Source -u -- $absolute 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "ConvertTo-CordumGpgArgPath: cygpath -u failed for '$absolute' (exit=$LASTEXITCODE): $converted"
    }
    $trimmed = ($converted | Out-String).Trim()
    if ([string]::IsNullOrEmpty($trimmed)) {
        throw "ConvertTo-CordumGpgArgPath: cygpath -u returned empty for '$absolute'"
    }
    return $trimmed
}
