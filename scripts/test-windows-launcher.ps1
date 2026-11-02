# Exercise the real launcher with a tiny engine and no UI/tool downloads.
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$goTool = (Get-Command go -CommandType Application).Source
$powerShell = (Get-Command powershell -CommandType Application).Source
$tempParent = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath()).TrimEnd('\')
$fixtureRoot = Join-Path $tempParent ('etape-launcher-' + [guid]::NewGuid())

try {
    New-Item -ItemType Directory -Path (Join-Path $fixtureRoot 'engine/cmd/etape'), (Join-Path $fixtureRoot 'ui/dist') -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $repoRoot 'run.ps1') -Destination $fixtureRoot
    Set-Content -LiteralPath (Join-Path $fixtureRoot 'engine/go.mod') -Value 'module etape-launcher-test'
    $engineSource = @'
package main

import (
    "encoding/json"
    "os"
)

func main() {
    exe, err := os.Executable()
    if err != nil { panic(err) }
    err = json.NewEncoder(os.Stdout).Encode(struct {
        Executable string
        Args []string
        Build string
    }{exe, os.Args[1:], "BUILD_MARKER"})
    if err != nil { panic(err) }
}
'@
    $harness = Join-Path $fixtureRoot 'harness.ps1'
    Set-Content -LiteralPath $harness -Value @'
$global:LauncherTestGoTool = $args[0]
$launcher = $args[1]
$launchArgs = @($args[2..($args.Count - 1)])
function global:npm { $global:LASTEXITCODE = 0 }
function global:go {
    if ($args[0] -eq 'run' -and $args[1] -like 'github.com/josephspurrier/goversioninfo/*') {
        $global:LASTEXITCODE = 0
        return
    }
    & $global:LauncherTestGoTool @args
    $global:LASTEXITCODE = $LASTEXITCODE
}
& $launcher @launchArgs
'@
    $cases = @(
        @{ Launch = @('live', '-config', 'C:\with spaces\config.toml'); Engine = @('-config', 'C:\with spaces\config.toml') },
        @{ Launch = @('demo', '42', '-no-open'); Engine = @('-demo', '-demo-seed', '42', '-no-open') },
        @{ Launch = @('demo', '-no-open'); Engine = @('-demo', '-no-open') }
    )
    $expectedExe = Join-Path $fixtureRoot 'engine/etape.exe'
    for ($i = 0; $i -lt $cases.Count; $i++) {
        Set-Content -LiteralPath (Join-Path $fixtureRoot 'engine/cmd/etape/main.go') -Value $engineSource.Replace('BUILD_MARKER', [string]$i)
        $launchArgs = $cases[$i].Launch
        $stderr = Join-Path $fixtureRoot 'stderr.log'
        $output = & $powerShell -NoProfile -ExecutionPolicy Bypass -File $harness $goTool (Join-Path $fixtureRoot 'run.ps1') @launchArgs 2> $stderr
        if ($LASTEXITCODE -ne 0) { throw "Launcher failed: $(Get-Content -LiteralPath $stderr -Raw)" }
        $result = $output | ConvertFrom-Json
        if ($result.Executable -ne $expectedExe -or -not (Test-Path -LiteralPath $expectedExe)) {
            throw "Taskbar target must survive launch/rebuild at $expectedExe; got $($result.Executable). Output: $output. Errors: $(Get-Content -LiteralPath $stderr -Raw)"
        }
        $expectedArgs = @('-dist', (Join-Path $fixtureRoot 'ui/dist')) + $cases[$i].Engine
        if (($result.Args | ConvertTo-Json -Compress) -ne ($expectedArgs | ConvertTo-Json -Compress) -or $result.Build -ne [string]$i) {
            throw "Launcher did not rebuild and forward arguments for ${launchArgs}: got $($result | ConvertTo-Json -Compress); expected args $($expectedArgs | ConvertTo-Json -Compress), build $i"
        }
        Write-Output "PASS: $launchArgs uses the persistent executable, rebuilt sources, and expected engine arguments"
    }
} finally {
    if (Test-Path -LiteralPath $fixtureRoot) {
        $resolved = (Resolve-Path -LiteralPath $fixtureRoot).Path
        if ([System.IO.Path]::GetDirectoryName($resolved) -ne $tempParent) { throw "Unsafe fixture cleanup path: $resolved" }
        Remove-Item -LiteralPath $resolved -Recurse -Force
    }
}
