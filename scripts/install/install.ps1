<#
nano-llm-proxy installer for Windows 10+ (local mode, per user, no admin rights).

  irm https://github.com/kmmuntasir/nano-llm-proxy/releases/latest/download/install.ps1 | iex

As a file (or through install.cmd):

  powershell -ExecutionPolicy Bypass -File install.ps1 [-Version vX.Y.Z] [-Port N]
      [-NoObscura] [-NoAutostart] [-NoOpen] [-Uninstall [-Purge]] [-Help]

`irm | iex` cannot take parameters; set environment variables instead:
  NANO_INSTALL_VERSION, NANO_INSTALL_PORT, and NANO_INSTALL_NO_OBSCURA,
  NANO_INSTALL_NO_AUTOSTART, NANO_INSTALL_NO_OPEN, NANO_INSTALL_UNINSTALL,
  NANO_INSTALL_PURGE (set to 1).
Or: & ([scriptblock]::Create((irm <url>/install.ps1))) -Port 9000

What it does:
  1. downloads nano-llm-proxy_<version>_windows_amd64.zip and checks its
     SHA-256 against the release's checksums.txt;
  2. installs it to %LOCALAPPDATA%\Programs\nano-llm-proxy (previous files
     kept as *.bak) and adds that folder to your user PATH;
  3. uses your obscura (the headless browser web_read uses for JavaScript-
     heavy pages) if one is on PATH, else downloads the pinned release into
     %LOCALAPPDATA%\nano-llm-proxy\bin and checks its SHA-256;
  4. runs `nano-llm-proxy.exe local install`, which starts nano-llm-proxy in
     the background, starts it when you sign in, and opens the browser.

Re-running upgrades in place. Test overrides: NANO_INSTALL_BASE_URL (a
directory URL holding the zip and checksums.txt), NANO_INSTALL_OBSCURA_BASE_URL
(a directory URL holding the obscura zip; it must match the pinned hash).
#>
param(
    [string]$Version = "",
    [int]$Port = 0,
    [switch]$NoObscura,
    [switch]$NoAutostart,
    [switch]$NoOpen,
    [switch]$Uninstall,
    [switch]$Purge,
    [switch]$Help
)

function Install-NanoLlmProxy {
    param(
        [string]$Version,
        [int]$Port,
        [bool]$NoObscura,
        [bool]$NoAutostart,
        [bool]$NoOpen,
        [bool]$Uninstall,
        [bool]$Purge,
        [bool]$Help
    )

    Set-StrictMode -Version 2.0
    $ErrorActionPreference = "Stop"
    # The progress bar makes Invoke-WebRequest many times slower on PowerShell 5.1.
    $ProgressPreference = "SilentlyContinue"

    $Repo = "kmmuntasir/nano-llm-proxy"
    $DefaultPort = 8787
    $InstallDir = Join-Path $env:LOCALAPPDATA "Programs\nano-llm-proxy"
    $Exe = Join-Path $InstallDir "nano-llm-proxy.exe"
    $Binaries = @("nano-llm-proxy.exe", "nano-llm-proxyw.exe")

    # Same pin and reason as install.sh: v0.2.4 has the Windows build and the
    # SSRF-gate and crash fixes over v0.2.3. obscura publishes no checksums
    # file, so the hash (the GitHub release's asset digest) is pinned here.
    # There is no Windows arm64 build.
    $ObscuraVersion = "v0.2.4"
    $ObscuraAsset = "obscura-x86_64-windows-stealth.zip"
    $ObscuraSha256 = "47d85a13fb1d19aca0fd71ebb3a1367248542f5344878b064c8586a193d604c9"
    $ObscuraBinaries = @("obscura.exe", "obscura-worker.exe")

    function Say([string]$msg) { Write-Host "==> $msg" -ForegroundColor Green }
    function Warn([string]$msg) { Write-Host "warning: $msg" -ForegroundColor Yellow }
    function Test-Flag([string]$v) { return @("1", "true", "yes") -contains "$v".ToLower() }

    function Show-Usage {
        Write-Host @"
nano-llm-proxy installer (Windows)

Options (file / install.cmd)   Environment (irm | iex)
  -Version vX.Y.Z              NANO_INSTALL_VERSION   install this release instead of the latest
  -Port N                      NANO_INSTALL_PORT      serve the GUI and API on port N (default 8787)
  -NoObscura                   NANO_INSTALL_NO_OBSCURA=1    skip obscura (renders JavaScript-heavy pages for web_read)
  -NoAutostart                 NANO_INSTALL_NO_AUTOSTART=1  do not start when you sign in
  -NoOpen                      NANO_INSTALL_NO_OPEN=1       do not open the browser at the end
  -Uninstall                   NANO_INSTALL_UNINSTALL=1     remove nano-llm-proxy
  -Purge                       NANO_INSTALL_PURGE=1         with -Uninstall: also delete the data folder
"@
    }

    # Processes started from our install folder (the background nano-llm-proxyw.exe,
    # or a nano-llm-proxy.exe left running in a terminal) lock the files we replace.
    function Stop-Installed {
        $procs = @(Get-Process -Name "nano-llm-proxyw", "nano-llm-proxy" -ErrorAction SilentlyContinue |
            Where-Object {
                $p = $null
                try { $p = $_.Path } catch { $p = $null }
                $p -and $p.StartsWith($InstallDir, [StringComparison]::OrdinalIgnoreCase)
            })
        if ($procs.Count -gt 0) {
            Say "Stopping the running nano-llm-proxy"
            $procs | Stop-Process -Force
            $procs | Wait-Process -Timeout 10 -ErrorAction SilentlyContinue
        }
    }

    # The user PATH is read and written raw (REG_EXPAND_SZ) so entries such as
    # %USERPROFILE%\... stay unexpanded; [Environment]::SetEnvironmentVariable
    # would rewrite the whole value expanded.
    function Get-UserPath {
        $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey("Environment")
        try {
            return [string]$key.GetValue("Path", "", [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        } finally { $key.Close() }
    }

    function Set-UserPath([string]$value) {
        $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey("Environment", $true)
        try {
            $key.SetValue("Path", $value, [Microsoft.Win32.RegistryValueKind]::ExpandString)
        } finally { $key.Close() }
        # Setting any user variable through .NET broadcasts WM_SETTINGCHANGE, so
        # terminals opened from now on see the new PATH without signing out.
        [Environment]::SetEnvironmentVariable("NANO_LLM_PROXY_INSTALLER", "1", "User")
        [Environment]::SetEnvironmentVariable("NANO_LLM_PROXY_INSTALLER", $null, "User")
    }

    function Test-SameDir([string]$a, [string]$b) {
        $a = [Environment]::ExpandEnvironmentVariables($a).TrimEnd("\")
        $b = [Environment]::ExpandEnvironmentVariables($b).TrimEnd("\")
        return [string]::Equals($a, $b, [StringComparison]::OrdinalIgnoreCase)
    }

    function Add-ToUserPath {
        $entries = @((Get-UserPath) -split ";" | Where-Object { $_ })
        if (@($entries | Where-Object { Test-SameDir $_ $InstallDir }).Count -eq 0) {
            Set-UserPath (($entries + $InstallDir) -join ";")
            Say "Added $InstallDir to your user PATH"
        }
        if (@($env:Path -split ";" | Where-Object { $_ -and (Test-SameDir $_ $InstallDir) }).Count -eq 0) {
            $env:Path = "$env:Path;$InstallDir"
        }
    }

    function Remove-FromUserPath {
        $entries = @((Get-UserPath) -split ";" | Where-Object { $_ })
        $kept = @($entries | Where-Object { -not (Test-SameDir $_ $InstallDir) })
        if ($kept.Count -ne $entries.Count) {
            Set-UserPath ($kept -join ";")
            Say "Removed $InstallDir from your user PATH"
        }
    }

    function Get-CurrentPort([string]$dataDir) {
        $envFile = Join-Path $dataDir ".env"
        $port = $DefaultPort
        if (Test-Path -LiteralPath $envFile) {
            foreach ($line in Get-Content -LiteralPath $envFile) {
                if ($line -match '^\s*(export\s+)?NANO_PORT\s*=\s*["'']?(\d+)') { $port = [int]$Matches[2] }
            }
        }
        return $port
    }

    # "0.2.4" from "obscura 0.2.4"; empty when it is missing or won't run.
    function Get-ObscuraVersion([string]$path) {
        if (-not (Test-Path -LiteralPath $path)) { return "" }
        $ErrorActionPreference = "Continue"
        try { $out = @(& $path --version 2>$null) } catch { return "" }
        if ($LASTEXITCODE -ne 0 -or $out.Count -eq 0) { return "" }
        $parts = @("$($out[0])".Trim() -split '\s+')
        if ($parts.Count -lt 2) { return "" }
        return $parts[1]
    }

    # The user's own obscura on PATH; one in our bin folder is ours to upgrade.
    function Find-UserObscura([string]$binDir) {
        $cmd = Get-Command obscura -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if (-not $cmd) { return "" }
        $p = [IO.Path]::GetFullPath($cmd.Source)
        if (Test-SameDir ([IO.Path]::GetDirectoryName($p)) $binDir) { return "" }
        return $p
    }

    # Returns $true when obscura.exe is in $binDir; failures warn, never throw.
    function Install-Obscura([string]$binDir) {
        $base = "$env:NANO_INSTALL_OBSCURA_BASE_URL".TrimEnd("/")
        if (-not $base) { $base = "https://github.com/h4ckf0r0day/obscura/releases/download/$ObscuraVersion" }
        $t = Join-Path ([IO.Path]::GetTempPath()) ("nano-llm-proxy-obscura-" + [Guid]::NewGuid().ToString("N"))
        try {
            New-Item -ItemType Directory -Path $t | Out-Null
            $zip = Join-Path $t $ObscuraAsset
            Say "  downloading obscura $ObscuraVersion (windows/amd64, about 75 MB)"
            try {
                Invoke-WebRequest -UseBasicParsing -Uri "$base/$ObscuraAsset" -OutFile $zip
            } catch {
                Warn "download failed: $base/$ObscuraAsset ($($_.Exception.Message))"
                return $false
            }
            $actual = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLowerInvariant()
            if ($actual -ne $ObscuraSha256) {
                Warn "checksum mismatch for $ObscuraAsset; not installing it (expected $ObscuraSha256, got $actual)"
                return $false
            }
            $x = Join-Path $t "extract"
            Expand-Archive -LiteralPath $zip -DestinationPath $x -Force
            if (-not (Test-Path -LiteralPath (Join-Path $x "obscura.exe"))) {
                Warn "$ObscuraAsset does not contain obscura.exe"
                return $false
            }
            New-Item -ItemType Directory -Path $binDir -Force | Out-Null
            foreach ($b in $ObscuraBinaries) {
                $src = Join-Path $x $b
                $dest = Join-Path $binDir $b
                if (Test-Path -LiteralPath $src) {
                    Copy-Item -LiteralPath $src -Destination "$dest.new" -Force
                    Move-Item -LiteralPath "$dest.new" -Destination $dest -Force
                } elseif (Test-Path -LiteralPath $dest) {
                    # never pair a new obscura with an old worker
                    Remove-Item -LiteralPath $dest -Force
                }
            }
            return $true
        } catch {
            # e.g. a running obscura.exe locks the file
            Warn "could not install obscura: $($_.Exception.Message)"
            return $false
        } finally {
            Remove-Item -LiteralPath $t -Recurse -Force -ErrorAction SilentlyContinue
        }
    }

    # Returns @{ Path = for --obscura (or ""); Status = for the summary }.
    function Set-UpObscura([string]$dataDir) {
        $r = @{ Path = ""; Status = "" }
        if ($NoObscura) { $r.Status = "not installed (-NoObscura)"; return $r }
        $binDir = Join-Path $dataDir "bin"
        $mine = Find-UserObscura $binDir
        if ($mine) {
            Say "Page rendering: using your obscura at $mine"
            $r.Path = $mine
            $r.Status = "$mine (yours)"
            return $r
        }
        $exe = Join-Path $binDir "obscura.exe"
        $want = $ObscuraVersion.TrimStart("v")
        $have = Get-ObscuraVersion $exe
        if ($have -eq $want) {
            Say "Page rendering: obscura $want already installed"
        } else {
            Say "Page rendering: installing obscura (the headless browser web_read uses for JavaScript-heavy pages)"
            if (Install-Obscura $binDir) {
                $have = Get-ObscuraVersion $exe
                if (-not $have) {
                    foreach ($b in $ObscuraBinaries) { Remove-Item -LiteralPath (Join-Path $binDir $b) -Force -ErrorAction SilentlyContinue }
                    Warn "the downloaded obscura does not run on this PC; removed it"
                    Warn "web_read still reads ordinary pages, only JavaScript-heavy ones fail"
                    $r.Status = "not installed (does not run here)"
                    return $r
                }
            } elseif ($have) {
                Warn "keeping the obscura $have already in $binDir"
            } else {
                Warn "obscura skipped: web_read still reads ordinary pages, only JavaScript-heavy ones fail"
                Warn "re-run the installer later to add it"
                $r.Status = "not installed (download failed)"
                return $r
            }
        }
        $r.Path = $exe
        $r.Status = "$exe (obscura $have)"
        return $r
    }

    function Invoke-Uninstall {
        if (Test-Path -LiteralPath $Exe) {
            $a = @("local", "uninstall")
            if ($Purge) { $a += "--purge" }
            Say "Removing the background service"
            & $Exe @a
            if ($LASTEXITCODE -ne 0) { throw "'nano-llm-proxy.exe $($a -join ' ')' failed; files were left in place" }
        } else {
            Warn "$Exe not found; no service to remove"
            if ($Purge) { Warn "the data folder was not deleted (it is found by the program); remove it by hand if it exists" }
        }
        Stop-Installed
        if (Test-Path -LiteralPath $InstallDir) {
            Remove-Item -LiteralPath $InstallDir -Recurse -Force
            Say "Removed $InstallDir"
        }
        Remove-FromUserPath
        if (-not $Purge) { Say "Your data folder was kept; uninstall again with -Purge to delete it" }
    }

    # --- arguments (parameters win, then environment variables) ---------------

    if (-not $Version) { $Version = "$env:NANO_INSTALL_VERSION" }
    if ($Port -eq 0 -and $env:NANO_INSTALL_PORT) {
        if ("$env:NANO_INSTALL_PORT" -notmatch '^\d+$') { throw "NANO_INSTALL_PORT must be a number" }
        $Port = [int]$env:NANO_INSTALL_PORT
    }
    $NoObscura = $NoObscura -or (Test-Flag $env:NANO_INSTALL_NO_OBSCURA)
    $NoAutostart = $NoAutostart -or (Test-Flag $env:NANO_INSTALL_NO_AUTOSTART)
    $NoOpen = $NoOpen -or (Test-Flag $env:NANO_INSTALL_NO_OPEN)
    $Uninstall = $Uninstall -or (Test-Flag $env:NANO_INSTALL_UNINSTALL)
    $Purge = $Purge -or (Test-Flag $env:NANO_INSTALL_PURGE)

    if ($Help) { Show-Usage; return }
    if ($Port -ne 0 -and ($Port -lt 1024 -or $Port -gt 65535)) { throw "-Port must be a number from 1024 to 65535" }
    if ($Purge -and -not $Uninstall) { throw "-Purge only works together with -Uninstall" }

    # --- platform -----------------------------------------------------------------

    if ([Environment]::OSVersion.Version.Major -lt 10) { throw "nano-llm-proxy needs Windows 10 or newer" }
    $arch = $env:PROCESSOR_ARCHITECTURE
    if ($env:PROCESSOR_ARCHITEW6432) { $arch = $env:PROCESSOR_ARCHITEW6432 }
    if ($arch -ne "AMD64") {
        throw "nano-llm-proxy for Windows is built for 64-bit Intel/AMD (amd64) only; this PC is '$arch'"
    }

    if ($Uninstall) { Invoke-Uninstall; return }

    # Windows PowerShell 5.1 on older Windows 10 builds may not offer TLS 1.2 by default.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    # --- download + verify --------------------------------------------------------

    $ver = $Version.TrimStart("v")
    if ($env:NANO_INSTALL_BASE_URL) {
        $base = "$env:NANO_INSTALL_BASE_URL".TrimEnd("/")
    } elseif ($ver) {
        $base = "https://github.com/$Repo/releases/download/v$ver"
    } else {
        # latest/download/<name> always serves the newest release's asset; the
        # version comes from the archive name listed in its checksums.txt.
        $base = "https://github.com/$Repo/releases/latest/download"
    }

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("nano-llm-proxy-install-" + [Guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        $sums = Join-Path $tmp "checksums.txt"
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "$base/checksums.txt" -OutFile $sums
        } catch {
            throw "download failed: $base/checksums.txt ($($_.Exception.Message))"
        }

        $asset = $null
        $expected = $null
        foreach ($line in Get-Content -LiteralPath $sums) {
            $parts = @($line.Trim() -split '\s+')
            if ($parts.Count -ne 2) { continue }
            $name = $parts[1]
            $wanted = if ($ver) { $name -eq "nano-llm-proxy_${ver}_windows_amd64.zip" } else { $name -match '^nano-llm-proxy_(.+)_windows_amd64\.zip$' }
            if ($wanted) {
                $asset = $name
                $expected = $parts[0]
                if (-not $ver) { $ver = $Matches[1] }
                break
            }
        }
        if (-not $asset) { throw "no Windows amd64 archive is listed in $base/checksums.txt" }

        Say "Downloading nano-llm-proxy v$ver (windows/amd64)"
        $zip = Join-Path $tmp $asset
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile $zip
        } catch {
            throw "download failed: $base/$asset ($($_.Exception.Message))"
        }
        $actual = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actual -ne $expected.ToLowerInvariant()) {
            throw "checksum mismatch for $asset - refusing to install (expected $expected, got $actual)"
        }
        Say "Checksum verified"

        $extract = Join-Path $tmp "extract"
        Expand-Archive -LiteralPath $zip -DestinationPath $extract -Force
        foreach ($b in $Binaries) {
            if (-not (Test-Path -LiteralPath (Join-Path $extract $b))) { throw "$asset does not contain $b" }
        }

        # --- install ------------------------------------------------------------------

        Stop-Installed
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
        foreach ($b in $Binaries) {
            $dest = Join-Path $InstallDir $b
            if (Test-Path -LiteralPath $dest) { Copy-Item -LiteralPath $dest -Destination "$dest.bak" -Force }
            Copy-Item -LiteralPath (Join-Path $extract $b) -Destination $dest -Force
        }
        & $Exe -version | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "the installed nano-llm-proxy.exe does not run on this PC" }
        Say "Installed $InstallDir"
    } finally {
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }

    Add-ToUserPath

    $dataDir = "$(& $Exe local datadir | Select-Object -Last 1)".Trim()
    if ($LASTEXITCODE -ne 0 -or -not $dataDir) { throw "'nano-llm-proxy.exe local datadir' failed (v$ver may predate local mode)" }

    $obscura = Set-UpObscura $dataDir

    $a = @("local", "install", "--quiet") # the summary below replaces its own
    if ($Port -ne 0) { $a += @("--port", "$Port") }
    if ($NoAutostart) { $a += "--no-autostart" }
    if ($NoOpen) { $a += "--no-open" }
    if ($obscura.Path) {
        # --obscura arrived with this installer; an older -Version binary lacks it
        if ("$(& $Exe local help)" -match '--obscura') {
            $a += @("--obscura", $obscura.Path)
        } else {
            Warn "nano-llm-proxy v$ver can't be told where obscura is; set Settings > Web tools > Obscura path to $($obscura.Path)"
        }
    }
    Say "Starting nano-llm-proxy"
    # Tee: show its output, and keep it for the summary
    $lio = $null
    & $Exe @a | Tee-Object -Variable lio | Write-Host
    if ($LASTEXITCODE -ne 0) { throw "'nano-llm-proxy.exe $($a -join ' ')' failed (see the message above)" }
    # a path the user set in Settings wins over ours (see `local install --obscura`)
    foreach ($line in @($lio)) {
        if ("$line" -match '^obscura: keeping your own path (.+) \(Settings > Web tools\)') {
            $obscura.Status = "$($Matches[1]) (yours, set in Settings > Web tools)"
        }
        if ("$line" -match '^warning: obscura path .* not saved') {
            $obscura.Status = "$($obscura.Path) (installed, but not saved to Settings: see the warning above)"
        }
    }

    $p = $Port
    if ($p -eq 0) { $p = Get-CurrentPort $dataDir }
    $url = "http://localhost:$p"
    Write-Host @"

nano-llm-proxy v$ver is installed.

  Admin GUI:            $url
  OpenAI base URL:      $url/v1
  Anthropic base URL:   $url
  API key:              any placeholder (e.g. "local") unless you turn on
                        "Require an API key" in Settings
  Web search:           needs a SearXNG URL in Settings > Web tools on Windows
                        (web_read works without it)
  obscura:              $($obscura.Status)
  Program folder:       $InstallDir
  Data folder:          $dataDir

Open a new terminal to use 'nano-llm-proxy' by name.

Uninstall (PowerShell):
  `$env:NANO_INSTALL_UNINSTALL=1; irm https://github.com/$Repo/releases/latest/download/install.ps1 | iex
  (also set `$env:NANO_INSTALL_PURGE=1 to delete the data folder)
"@
}

# A file run (-File, install.cmd) reports failure through the exit code; under
# `irm | iex` an exit would close the user's PowerShell window, so it doesn't.
$nanoRunAsFile = $MyInvocation.MyCommand.CommandType -eq "ExternalScript"
try {
    Install-NanoLlmProxy -Version $Version -Port $Port -NoObscura $NoObscura.IsPresent -NoAutostart $NoAutostart.IsPresent `
        -NoOpen $NoOpen.IsPresent -Uninstall $Uninstall.IsPresent -Purge $Purge.IsPresent -Help $Help.IsPresent
} catch {
    Write-Host "error: $($_.Exception.Message)" -ForegroundColor Red
    if ($nanoRunAsFile) { exit 1 }
}
