[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [Alias("d", "dest")]
    [string]$Destination,

    [Parameter(Position = 1)]
    [Alias("t", "v", "version")]
    [string]$Tag = "latest"
)

$ErrorActionPreference = "Stop"

function Terminate-Script {
    param(
        [string]$Message,
        [int]$ExitCode = 1
    )

    if (-not $PSCommandPath) {
        # Running via iex or pipeline: throw avoids terminating the interactive caller's host shell
        if ($Message) {
            throw $Message
        } else {
            throw "Script failed with exit code $ExitCode"
        }
    }

    if ($Message) {
        [Console]::Error.WriteLine($Message)
    }
    exit $ExitCode
}

if ($Destination -match '^(latest|v?[0-9]+\.[0-9]+)' -and $Tag -eq "latest") {
    $Tag = $Destination
    $Destination = $null
}

if (-not $Destination) {
    if ($env:USERPROFILE) {
        $Destination = Join-Path $env:USERPROFILE ".local\bin"
    } elseif ($HOME) {
        $Destination = Join-Path $HOME ".local\bin"
    } else {
        $Destination = Join-Path (Get-Location) ".local\bin"
    }
}

if ($Destination.StartsWith("~")) {
    $homeDir = if ($env:USERPROFILE) { $env:USERPROFILE } elseif ($HOME) { $HOME } else { "." }
    $Destination = Join-Path $homeDir $Destination.Substring(1).TrimStart('\', '/')
}

$detectedOs = $null
if ($IsWindows -or $env:OS -eq "Windows_NT" -or [System.Environment]::OSVersion.Platform -eq "Win32NT") {
    $detectedOs = "windows"
} elseif ($IsLinux) {
    $detectedOs = "linux"
} elseif ($IsMacOS) {
    $detectedOs = "darwin"
} else {
    try {
        if ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Windows)) {
            $detectedOs = "windows"
        } elseif ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Linux)) {
            $detectedOs = "linux"
        } elseif ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::OSX)) {
            $detectedOs = "darwin"
        }
    } catch {
        # Fallback
    }
}

if (-not $detectedOs) {
    Terminate-Script "Error: Unsupported operating system (only linux, darwin, windows are supported)"
}

$detectedArch = $null
$archStr = ""
try {
    $archStr = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
} catch {
    $archStr = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
}

if ($archStr -match '^(X64|AMD64)$') {
    $detectedArch = "amd64"
} elseif ($archStr -match '^(Arm64|ARM64)$') {
    $detectedArch = "arm64"
} else {
    Terminate-Script "Error: Unsupported architecture: $archStr (only amd64, arm64 are supported)"
}

if ($detectedOs -eq "windows") {
    $exeName = "mcp-relayd.exe"
    $suffix = "zip"
} else {
    $exeName = "mcp-relayd"
    $suffix = "tar.gz"
}

function Download-File {
    param(
        [string]$Url,
        [string]$OutputFile
    )

    if (-not $Url.StartsWith("https://", [System.StringComparison]::OrdinalIgnoreCase)) {
        Terminate-Script "Error: Insecure download URL rejected: $Url (HTTPS required)"
    }

    try {
        [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.SecurityProtocolType]::Tls12 -bor [System.Net.SecurityProtocolType]::Tls13
    } catch {
        [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.SecurityProtocolType]::Tls12
    }

    Invoke-WebRequest -Uri $Url -OutFile $OutputFile -UseBasicParsing
}

$repoUrl = "https://github.com/atlasfoo/mcp-relayd"

if ($Tag -eq "latest") {
    $sumsUrl = "$repoUrl/releases/latest/download/SHA256SUMS"
    $baseDownloadUrl = "$repoUrl/releases/latest/download"
} else {
    $releaseTag = $Tag
    if ($releaseTag -match '^[0-9]') {
        $releaseTag = "v$releaseTag"
    }
    $sumsUrl = "$repoUrl/releases/download/$releaseTag/SHA256SUMS"
    $baseDownloadUrl = "$repoUrl/releases/download/$releaseTag"
}

$tempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("mcp-relayd-install-" + [System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tempDir -Force | Out-Null

try {
    $sumsFile = Join-Path $tempDir "SHA256SUMS"
    try {
        Download-File -Url $sumsUrl -OutputFile $sumsFile
    } catch {
        if ($Tag -ne "latest" -and $releaseTag -ne $Tag) {
            $sumsUrl = "$repoUrl/releases/download/$Tag/SHA256SUMS"
            $baseDownloadUrl = "$repoUrl/releases/download/$Tag"
            try {
                Download-File -Url $sumsUrl -OutputFile $sumsFile
            } catch {
                Terminate-Script "Error: Failed to download SHA256SUMS from ${sumsUrl}: $_"
            }
        } else {
            Terminate-Script "Error: Failed to download SHA256SUMS from ${sumsUrl}: $_"
        }
    }

    $expectedHash = $null
    $assetName = $null
    $sumsContent = Get-Content -Path $sumsFile

    if ($Tag -ne "latest") {
        $version = if ($Tag.StartsWith("v")) { $Tag.Substring(1) } else { $Tag }
        $targetName = "mcp-relayd_${version}_${detectedOs}_${detectedArch}.${suffix}"
        foreach ($line in $sumsContent) {
            $parts = $line.Trim() -split '\s+'
            if ($parts.Length -ge 2) {
                $name = $parts[1].TrimStart('*')
                if ($name -eq $targetName) {
                    $expectedHash = $parts[0].ToLower()
                    $assetName = $name
                    break
                }
            }
        }
    } else {
        $targetEnding = "_${detectedOs}_${detectedArch}.${suffix}"
        $matchCount = 0
        foreach ($line in $sumsContent) {
            $parts = $line.Trim() -split '\s+'
            if ($parts.Length -ge 2) {
                $name = $parts[1].TrimStart('*')
                if ($name.EndsWith($targetEnding)) {
                    $matchCount++
                    $expectedHash = $parts[0].ToLower()
                    $assetName = $name
                }
            }
        }
        if ($matchCount -ne 1) {
            $expectedHash = $null
            $assetName = $null
        }
    }

    if (-not $expectedHash -or -not $assetName -or $expectedHash.Length -ne 64 -or -not ($expectedHash -match '^[0-9a-fA-F]{64}$')) {
        Terminate-Script "Error: Checksum for asset ${detectedOs}/${detectedArch} (${suffix}) not found or invalid in SHA256SUMS"
    }

    $assetFile = Join-Path $tempDir $assetName
    $assetUrl = "$baseDownloadUrl/$assetName"
    try {
        Download-File -Url $assetUrl -OutputFile $assetFile
    } catch {
        Terminate-Script "Error: Failed to download asset from ${assetUrl}: $_"
    }

    $actualHash = (Get-FileHash -Path $assetFile -Algorithm SHA256).Hash.ToLower()
    if ($actualHash -ne $expectedHash) {
        $mismatchMsg = "Error: Checksum mismatch for $assetName`nExpected: $expectedHash`nActual:   $actualHash"
        Terminate-Script $mismatchMsg
    }

    $extractDir = Join-Path $tempDir "extracted"
    New-Item -ItemType Directory -Path $extractDir -Force | Out-Null

    if ($suffix -eq "zip") {
        Expand-Archive -Path $assetFile -DestinationPath $extractDir -Force
    } else {
        tar -xzf $assetFile -C $extractDir
    }

    $extractedExe = Join-Path $extractDir $exeName
    if (-not (Test-Path -Path $extractedExe)) {
        Terminate-Script "Error: Executable $exeName was not found in archive $assetName"
    }

    if (-not (Test-Path -Path $Destination)) {
        New-Item -ItemType Directory -Path $Destination -Force | Out-Null
    }

    $targetPath = Join-Path $Destination $exeName
    Copy-Item -Path $extractedExe -Destination $targetPath -Force

    $destResolved = (Resolve-Path -Path $Destination).Path
    $pathEntries = ($env:PATH -split [System.IO.Path]::PathSeparator)
    $inPath = $false
    foreach ($p in $pathEntries) {
        if (-not [string]::IsNullOrWhiteSpace($p)) {
            try {
                if ((Resolve-Path -Path $p -ErrorAction SilentlyContinue).Path -eq $destResolved) {
                    $inPath = $true
                    break
                }
            } catch {}
        }
    }

    if (-not $inPath) {
        [Console]::Error.WriteLine("")
        [Console]::Error.WriteLine("========================================================================")
        [Console]::Error.WriteLine("WARNING: $Destination is not in your PATH!")
        [Console]::Error.WriteLine("You will not be able to run 'mcp-relayd' directly from the terminal.")
        [Console]::Error.WriteLine("Please add $Destination to your PATH environment variable.")
        [Console]::Error.WriteLine("========================================================================")
        [Console]::Error.WriteLine("")
    }

    Write-Host "Installed $exeName to $targetPath"
    Write-Host ""
    Write-Host "Next steps:"
    Write-Host "1. Run 'mcp-relayd config' to generate a default configuration file."
    Write-Host "2. Refer to https://github.com/atlasfoo/mcp-relayd/blob/master/README.md for setup and usage instructions."
} finally {
    Remove-Item -Path $tempDir -Recurse -Force -ErrorAction SilentlyContinue
}
