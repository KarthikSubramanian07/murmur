# Installs t4 on Windows: downloads the latest release's binary for this machine, checks it against
# the release's SHA256SUMS, puts it in %USERPROFILE%\.local\bin, and adds that folder to your user
# PATH if it isn't there. It needs no administrator rights. macOS and Linux have install.sh.
#
#   irm https://raw.githubusercontent.com/a-Fig/t4/main/install.ps1 | iex
#
# Environment:
#   T4_VERSION    install this release (v0.1.0 or 0.1.0) instead of the latest stable one
#   GITHUB_TOKEN  download through the GitHub API, which works while the repo is private
#   T4_REPO       fetch releases of this repo (owner/name) instead of a-Fig/t4
#
# docs/releasing.md says where the binaries come from and what the checksum does and doesn't prove.
#
# The work is in a script block so that running this through iex doesn't leave variables behind
# in your session, and it throws rather than exits, because exit would close your window.
& {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'   # the progress bar makes Windows PowerShell 5.1 downloads far slower
    # Windows PowerShell 5.1 may not offer TLS 1.2 by default, and GitHub requires it.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $repo = if ($env:T4_REPO) { $env:T4_REPO } else { 'a-Fig/t4' }
    $token = $env:GITHUB_TOKEN
    $tag = $null
    if ($env:T4_VERSION) {
        $tag = if ($env:T4_VERSION.StartsWith('v')) { $env:T4_VERSION } else { "v$($env:T4_VERSION)" }
    }

    # A 32-bit PowerShell on 64-bit Windows reports x86 here; PROCESSOR_ARCHITEW6432 has the real one.
    $machine = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    switch ($machine) {
        'AMD64' { $arch = 'amd64' }
        'ARM64' { $arch = 'arm64' }
        default { throw "t4 install: unsupported architecture $machine; t4 has Windows binaries for amd64 and arm64." }
    }
    $asset = "t4-windows-$arch.exe"

    $dir = Join-Path $env:USERPROFILE '.local\bin'
    $exe = Join-Path $dir 't4.exe'
    $old = $null
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("t4-install-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null

    try {
        if ($token) {
            # releases/latest/download needs no login only on a public repo; a private one is read
            # through the API. The API names assets by id, and sends the file itself from a redirect
            # that must not get the token (its storage host rejects a request that carries two kinds
            # of credentials), so the redirect is followed by hand.
            $api = "https://api.github.com/repos/$repo/releases/" + $(if ($tag) { "tags/$tag" } else { 'latest' })
            $headers = @{ Authorization = "Bearer $token"; Accept = 'application/vnd.github+json'; 'User-Agent' = 't4-install' }
            try {
                $release = Invoke-RestMethod -Uri $api -Headers $headers
            } catch {
                throw "t4 install: could not read the release at $api ($($_.Exception.Message)). Is the token valid, with access to $repo, and does the release exist? 'latest' skips prereleases; pin one with T4_VERSION. To download without a token, remove GITHUB_TOKEN."
            }
            function Get-Asset($name) {
                $entry = @($release.assets | Where-Object { $_.name -eq $name })[0]
                if (-not $entry) { throw "t4 install: the release has no asset named $name." }
                $request = [Net.HttpWebRequest]::Create($entry.url)
                $request.AllowAutoRedirect = $false
                $request.UserAgent = 't4-install'
                $request.Accept = 'application/octet-stream'
                $request.Headers.Add('Authorization', "Bearer $token")
                try {
                    $response = $request.GetResponse()
                } catch {
                    throw "t4 install: could not start the download of ${name}: $($_.Exception.Message)"
                }
                # The answer is either the file itself (200) or a redirect to it (3xx).
                try {
                    if ([int]$response.StatusCode -eq 200) {
                        $file = [IO.File]::Create((Join-Path $tmp $name))
                        try { $response.GetResponseStream().CopyTo($file) } finally { $file.Close() }
                        return
                    }
                    $location = $response.Headers['Location']
                } catch {
                    throw "t4 install: the download of $name failed: $($_.Exception.Message)"
                } finally {
                    $response.Close()
                }
                if (-not $location) { throw "t4 install: GitHub did not say where to download $name from." }
                try {
                    Invoke-WebRequest -UseBasicParsing -Uri $location -OutFile (Join-Path $tmp $name)
                } catch {
                    throw "t4 install: the download of $name failed: $($_.Exception.Message)"
                }
            }
        } else {
            $base = if ($tag) { "https://github.com/$repo/releases/download/$tag" } else { "https://github.com/$repo/releases/latest/download" }
            function Get-Asset($name) {
                try {
                    Invoke-WebRequest -UseBasicParsing -Uri "$base/$name" -OutFile (Join-Path $tmp $name)
                } catch {
                    throw "t4 install: could not download $base/${name}: $($_.Exception.Message). Is the release published, and is $repo public? Otherwise set GITHUB_TOKEN."
                }
            }
        }

        Write-Host "Downloading $asset$(if ($tag) { " ($tag)" }) from $repo..."
        Get-Asset $asset
        Get-Asset 'SHA256SUMS'

        # SHA256SUMS lines read "<hash>  <name>" or, for binary files, "<hash> *<name>".
        $want = $null
        foreach ($line in Get-Content (Join-Path $tmp 'SHA256SUMS')) {
            if ($line -match '^([0-9a-fA-F]{64})\s+\*?(.+?)\s*$' -and $Matches[2] -eq $asset) { $want = $Matches[1].ToLower(); break }
        }
        if (-not $want) { throw "t4 install: SHA256SUMS has no line for $asset." }
        $got = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $tmp $asset)).Hash.ToLower()
        if ($got -ne $want) { throw "t4 install: the checksum of $asset does not match SHA256SUMS (wanted $want, got $got); nothing was installed." }
        Write-Host 'Checksum OK.'

        New-Item -ItemType Directory -Force -Path $dir | Out-Null
        # Windows won't overwrite or delete a running exe (t4's daemon keeps one running), but it will
        # rename one. So the old file is moved aside, the new one put in its place, and the old one
        # deleted if nothing holds it; otherwise the next install deletes it.
        Get-ChildItem -Path $dir -Filter 't4.exe.old-*' -ErrorAction SilentlyContinue | Remove-Item -Force -ErrorAction SilentlyContinue
        if (Test-Path $exe) {
            $old = "$exe.old-$([Guid]::NewGuid().ToString('N').Substring(0, 8))"
            Move-Item -Force -Path $exe -Destination $old
        }
        Move-Item -Force -Path (Join-Path $tmp $asset) -Destination $exe
        if ($old) { Remove-Item -Force -Path $old -ErrorAction SilentlyContinue }
    } finally {
        Remove-Item -Recurse -Force -Path $tmp -ErrorAction SilentlyContinue
    }

    # Edit the user PATH in the registry. [Environment]::SetEnvironmentVariable would also work, but
    # it writes back the expanded value, turning an entry like %USERPROFILE%\go\bin into a fixed path.
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
    try {
        $names = [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames
        $current = [string]$key.GetValue('Path', '', $names)
        $kind = if ($null -ne $key.GetValue('Path', $null)) { $key.GetValueKind('Path') } else { [Microsoft.Win32.RegistryValueKind]::ExpandString }
        $present = $false
        foreach ($entry in ($current -split ';')) {
            if ($entry -and ([Environment]::ExpandEnvironmentVariables($entry).TrimEnd('\') -ieq $dir.TrimEnd('\'))) { $present = $true }
        }
        if (-not $present) {
            $key.SetValue('Path', ($(if ($current) { $current.TrimEnd(';') + ';' } else { '' }) + $dir), $kind)
            # Writing the registry doesn't tell running programs; setting any user variable does.
            [Environment]::SetEnvironmentVariable('T4_INSTALL_REFRESH', '1', 'User')
            [Environment]::SetEnvironmentVariable('T4_INSTALL_REFRESH', $null, 'User')
        }
    } finally {
        $key.Close()
    }
    $onSessionPath = @($env:Path -split ';' | Where-Object { $_.TrimEnd('\') -ieq $dir.TrimEnd('\') }).Count -gt 0
    if (-not $onSessionPath) { $env:Path = "$dir;$env:Path" }

    & $exe version
    if ($LASTEXITCODE -ne 0) { throw "t4 install: installed $exe but it did not run; the file is at that path." }
    Write-Host "Installed: $exe"
    if ($present) {
        Write-Host "$dir is already on your user PATH."
    } else {
        Write-Host "Added $dir to your user PATH. Terminals opened from now on find t4; this window does too."
    }
}
