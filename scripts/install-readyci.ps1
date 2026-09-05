$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0

$version = if ($env:READYCI_VERSION) { $env:READYCI_VERSION } else { 'latest' }
$installDir = if ($env:READYCI_INSTALL_DIR) {
    $env:READYCI_INSTALL_DIR
} else {
    Join-Path $env:LOCALAPPDATA 'Programs\ReadyCI'
}
$baseURL = if ($env:READYCI_RELEASE_BASE_URL) { $env:READYCI_RELEASE_BASE_URL } else { '' }
$embeddedMinisignPublicKey = '__READYCI_MINISIGN_PUBLIC_KEY__'
$minisignPublicKey = if ($env:READYCI_MINISIGN_PUBLIC_KEY) {
    $env:READYCI_MINISIGN_PUBLIC_KEY
} else {
    $embeddedMinisignPublicKey
}
$allowUnsignedInstall = $env:READYCI_ALLOW_UNSIGNED_INSTALL
$versionPattern = (
    '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)' +
    '(-((0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))' +
    '(\.((0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)))*)?$'
)
$updatePath = if (
    $null -eq $env:READYCI_INSTALL_UPDATE_PATH -or
    $env:READYCI_INSTALL_UPDATE_PATH -eq ''
) {
    '1'
} else {
    $env:READYCI_INSTALL_UPDATE_PATH
}

if ($minisignPublicKey -eq '__READYCI_MINISIGN_PUBLIC_KEY__') {
    $minisignPublicKey = ''
}
if ($updatePath -notin @('0', '1')) {
    throw 'READYCI_INSTALL_UPDATE_PATH must be 0 or 1'
}
if (
    $version -ne 'latest' -and
    ($version.Length -gt 64 -or $version -cnotmatch $versionPattern)
) {
    throw 'READYCI_VERSION must be latest or a canonical vMAJOR.MINOR.PATCH[-PRERELEASE] version'
}

$architecture = if ($env:PROCESSOR_ARCHITEW6432) {
    $env:PROCESSOR_ARCHITEW6432
} else {
    $env:PROCESSOR_ARCHITECTURE
}
$arch = switch ($architecture.ToUpperInvariant()) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { throw "Unsupported Windows architecture: $architecture" }
}

if ([string]::IsNullOrWhiteSpace($baseURL)) {
    if ($version -eq 'latest') {
        $baseURL = 'https://github.com/DeanStr/readyci-cli/releases/latest/download'
    } else {
        $baseURL = "https://github.com/DeanStr/readyci-cli/releases/download/$version"
    }
}
$releaseDirectory = $null
$releaseBaseURI = $null
if (Test-Path -LiteralPath $baseURL -PathType Container) {
    $releaseDirectory = [IO.Path]::GetFullPath($baseURL)
} else {
    $uri = $null
    if (-not [Uri]::TryCreate($baseURL, [UriKind]::Absolute, [ref] $uri)) {
        throw 'READYCI_RELEASE_BASE_URL must be an HTTPS URL, file URL, or existing local directory'
    }
    if ($uri.Scheme -eq 'file') {
        if (-not [string]::IsNullOrEmpty($uri.Query) -or -not [string]::IsNullOrEmpty($uri.Fragment)) {
            throw 'READYCI_RELEASE_BASE_URL file URLs must not contain a query or fragment'
        }
        $releaseDirectory = [IO.Path]::GetFullPath($uri.LocalPath)
        if (-not (Test-Path -LiteralPath $releaseDirectory -PathType Container)) {
            throw 'READYCI_RELEASE_BASE_URL file URL does not identify an existing directory'
        }
    } elseif ($uri.Scheme -eq 'https') {
        if (
            -not [string]::IsNullOrEmpty($uri.UserInfo) -or
            -not [string]::IsNullOrEmpty($uri.Query) -or
            -not [string]::IsNullOrEmpty($uri.Fragment)
        ) {
            throw 'READYCI_RELEASE_BASE_URL HTTPS URLs must not contain userinfo, a query, or a fragment'
        }
        $releaseBaseURI = $uri.AbsoluteUri.TrimEnd('/')
    } else {
        throw "Unsupported release URL scheme: $($uri.Scheme)"
    }
}
$archive = "readyci_windows_$arch.zip"
$tempDir = Join-Path ([IO.Path]::GetTempPath()) ("readyci-cli-" + [Guid]::NewGuid().ToString('N'))

function Copy-ReleaseObject {
    param(
        [Parameter(Mandatory)] [string] $Name,
        [Parameter(Mandatory)] [string] $Destination
    )

    if ($null -ne $releaseDirectory) {
        Copy-Item -LiteralPath (Join-Path $releaseDirectory $Name) -Destination $Destination
        return
    }

    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    Invoke-WebRequest -UseBasicParsing -Uri "$releaseBaseURI/$Name" -OutFile $Destination
}

try {
    New-Item -ItemType Directory -Force -Path $tempDir | Out-Null
    $archivePath = Join-Path $tempDir $archive
    $checksumsPath = Join-Path $tempDir 'SHA256SUMS'
    Copy-ReleaseObject -Name $archive -Destination $archivePath
    Copy-ReleaseObject -Name 'SHA256SUMS' -Destination $checksumsPath

    if ([string]::IsNullOrWhiteSpace($minisignPublicKey)) {
        if ($allowUnsignedInstall -ne '1') {
            throw (
                'Ready CI installer signature verification is not configured. ' +
                'Use the generated release install.ps1, set READYCI_MINISIGN_PUBLIC_KEY, ' +
                'or set READYCI_ALLOW_UNSIGNED_INSTALL=1 to accept checksum-only installation.'
            )
        }
        Write-Warning (
            'Ready CI installer is running without signature verification; ' +
            'SHA256SUMS is trusted from the same origin as the archive.'
        )
    } else {
        $signaturePath = Join-Path $tempDir 'SHA256SUMS.minisig'
        Copy-ReleaseObject -Name 'SHA256SUMS.minisig' -Destination $signaturePath
        $minisign = Get-Command minisign.exe -CommandType Application -ErrorAction SilentlyContinue
        if ($null -eq $minisign) {
            $minisign = Get-Command minisign -CommandType Application -ErrorAction SilentlyContinue
        }
        if ($null -eq $minisign) {
            throw 'minisign is required to verify SHA256SUMS'
        }
        & $minisign.Source -Vm $checksumsPath -x $signaturePath -P $minisignPublicKey | Out-Null
        if ($LASTEXITCODE -ne 0) {
            throw 'Minisign verification failed for SHA256SUMS'
        }
    }

    $expected = $null
    $seen = @{}
    foreach ($line in [IO.File]::ReadAllLines($checksumsPath, [Text.Encoding]::ASCII)) {
        if ($line -notmatch '^([0-9a-f]{64}) [ *]([A-Za-z0-9][A-Za-z0-9._-]*)$') {
            throw 'SHA256SUMS contains a malformed line'
        }
        $name = $Matches[2]
        if ($seen.ContainsKey($name)) {
            throw "SHA256SUMS contains a duplicate entry for $name"
        }
        $seen[$name] = $Matches[1]
        if ($name -eq $archive) {
            $expected = $Matches[1]
        }
    }
    if ([string]::IsNullOrWhiteSpace($expected)) {
        throw "Checksum for $archive was not found in SHA256SUMS"
    }
    $actual = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $expected) {
        throw "Checksum mismatch for $archive"
    }

    $expanded = Join-Path $tempDir 'expanded'
    Expand-Archive -LiteralPath $archivePath -DestinationPath $expanded
    $sourceBinary = Join-Path $expanded 'readyci.exe'
    if (-not (Test-Path -LiteralPath $sourceBinary -PathType Leaf)) {
        throw "$archive does not contain readyci.exe"
    }

    $installDir = [IO.Path]::GetFullPath($installDir)
    $installRoot = [IO.Path]::GetPathRoot($installDir)
    if ($installDir.TrimEnd('\') -eq $installRoot.TrimEnd('\')) {
        throw "Refusing unsafe installation directory: $installDir"
    }
    New-Item -ItemType Directory -Force -Path $installDir | Out-Null
    $installDirInfo = Get-Item -LiteralPath $installDir -Force
    if (($installDirInfo.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "Refusing installation through a reparse-point directory: $installDir"
    }
    $destination = Join-Path $installDir 'readyci.exe'
    $staged = Join-Path $installDir ('.readyci-' + [Guid]::NewGuid().ToString('N') + '.exe')
    $backup = Join-Path $installDir ('.readyci-backup-' + [Guid]::NewGuid().ToString('N') + '.tmp')
    $hadExisting = Test-Path -LiteralPath $destination
    $replacementAttempted = $false
    $removeBackup = $false
    try {
        if ($hadExisting) {
            $destinationInfo = Get-Item -LiteralPath $destination -Force
            if (
                $destinationInfo.PSIsContainer -or
                ($destinationInfo.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0
            ) {
                throw "Refusing to replace a non-regular installation target: $destination"
            }
        }

        Copy-Item -LiteralPath $sourceBinary -Destination $staged
        Unblock-File -LiteralPath $staged -ErrorAction SilentlyContinue
        $candidateVersion = ((& $staged version) | Out-String).Trim()
        $candidateExitCode = $LASTEXITCODE
        if ($candidateExitCode -ne 0) {
            throw "Downloaded readyci failed its version probe with exit $candidateExitCode"
        }
        if (
            [string]::IsNullOrWhiteSpace($candidateVersion) -or
            $candidateVersion.Length -gt 64 -or
            $candidateVersion -cnotmatch $versionPattern
        ) {
            throw "Downloaded readyci returned an invalid version: $candidateVersion"
        }
        if ($version -ne 'latest' -and $candidateVersion -cne $version) {
            throw "Downloaded readyci version $candidateVersion does not match requested version $version"
        }

        if ($hadExisting) {
            Copy-Item -LiteralPath $destination -Destination $backup
        }
        $replacementAttempted = $true
        Move-Item -LiteralPath $staged -Destination $destination -Force
        $installedVersion = ((& $destination version) | Out-String).Trim()
        $installedExitCode = $LASTEXITCODE
        if ($installedExitCode -ne 0) {
            throw "Installed readyci failed its version probe with exit $installedExitCode"
        }
        if ($installedVersion -cne $candidateVersion) {
            throw (
                "Installed readyci version $installedVersion does not match " +
                "the verified candidate version $candidateVersion"
            )
        }
        $removeBackup = $true
    } catch {
        $installFailure = $_
        try {
            if ($hadExisting -and -not $replacementAttempted) {
                $removeBackup = $true
            } elseif ($hadExisting) {
                if (-not (Test-Path -LiteralPath $backup -PathType Leaf)) {
                    throw "The previous executable backup is unavailable: $backup"
                }
                $destinationMatchesBackup = $false
                if (Test-Path -LiteralPath $destination -PathType Leaf) {
                    $destinationMatchesBackup = (
                        (Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash -ceq
                        (Get-FileHash -LiteralPath $backup -Algorithm SHA256).Hash
                    )
                }
                if (-not $destinationMatchesBackup) {
                    if (Test-Path -LiteralPath $destination) {
                        Remove-Item -LiteralPath $destination -Force
                    }
                    Copy-Item -LiteralPath $backup -Destination $destination
                    $destinationMatchesBackup = (
                        (Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash -ceq
                        (Get-FileHash -LiteralPath $backup -Algorithm SHA256).Hash
                    )
                    if (-not $destinationMatchesBackup) {
                        throw 'The restored executable does not match its backup'
                    }
                }
                $removeBackup = $true
            } elseif (-not $hadExisting) {
                if (Test-Path -LiteralPath $destination) {
                    Remove-Item -LiteralPath $destination -Force
                }
                $removeBackup = $true
            }
        } catch {
            if (-not $hadExisting) {
                throw (
                    "Ready CI installation failed ($($installFailure.Exception.Message)) and " +
                    "the failed executable could not be removed: $($_.Exception.Message)"
                )
            }
            $backupMessage = if (Test-Path -LiteralPath $backup -PathType Leaf) {
                "The previous executable backup was preserved at $backup."
            } else {
                "No usable previous executable backup remains at $backup."
            }
            throw (
                "Ready CI installation failed ($($installFailure.Exception.Message)) and " +
                "the previous executable could not be restored: $($_.Exception.Message). " +
                $backupMessage
            )
        }
        throw $installFailure
    } finally {
        Remove-Item -LiteralPath $staged -Force -ErrorAction SilentlyContinue
        if ($removeBackup) {
            Remove-Item -LiteralPath $backup -Force -ErrorAction SilentlyContinue
        }
    }

    if ($updatePath -eq '1') {
        $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        $entries = @($userPath -split ';' | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
        if (-not ($entries | Where-Object { $_.TrimEnd('\') -ieq $installDir.TrimEnd('\') })) {
            $newPath = (@($entries) + $installDir) -join ';'
            [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        }
        $processPathContainsInstallDir = ($env:Path -split ';') | Where-Object {
            $_.TrimEnd('\') -ieq $installDir.TrimEnd('\')
        }
        if (-not $processPathContainsInstallDir) {
            $env:Path = "$installDir;$env:Path"
        }
    }
    Write-Output "installed readyci to $destination"
    Write-Output "version: $installedVersion"
    if ($updatePath -eq '1') {
        Write-Output 'PATH was updated for future user sessions.'
    }
} finally {
    Remove-Item -LiteralPath $tempDir -Recurse -Force -ErrorAction SilentlyContinue
}
