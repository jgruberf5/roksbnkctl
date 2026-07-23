<#
.SYNOPSIS
  Native-Windows (no WSL) validation for roksbnkctl after the tfx conversion.

.DESCRIPTION
  Proves that roksbnkctl drives terraform + helm on a stock Windows host with NO
  bash / WSL / curl / grep / python. It:
    1. Preflight: confirms terraform, helm, roksbnkctl.exe are on PATH and that
       wsl.exe / bash.exe are ABSENT (so any success is genuinely native).
    2. Seeds a workspace from a supplied config.yaml.
    3. Runs the requested roksbnkctl phases (attach to an existing cluster to skip
       the ~40m create), capturing all output.
    4. Extracts every 'tfx' subcommand invocation and null_resource result from the
       terraform output, so we can see the Go helpers ran (not a shell).
    5. Emits machine-consumable results: results.json + transcript.log + tfx-calls.log.

  Run from an elevated-or-normal PowerShell (5.1+ or 7+). Set the IBM key first:
      $env:IBMCLOUD_API_KEY = '<key>'
      .\run-windows-test.ps1 -ConfigFile .\config.yaml -Phases cluster,flp,bnk

.PARAMETER ConfigFile
  Path to a ready roksbnkctl config.yaml (REQUIRED). tf_source.type should be
  'embedded' so the binary's own (tfx-converted) modules are exercised.

.PARAMETER RoksbnkctlExe
  Path to roksbnkctl.exe. Default: 'roksbnkctl' resolved on PATH.

.PARAMETER Workspace
  Workspace name to seed and drive. Default: winctest.

.PARAMETER Phases
  Comma list of phases to run, in order. Allowed: cluster, flp, bnk, gateway.
  Default: cluster,bnk

.PARAMETER OutDir
  Output directory for artifacts. Default: .\windows-test-out

.PARAMETER TerraformLogDebug
  If set, exports TF_LOG=DEBUG for maximum local-exec visibility (verbose).
#>
[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)][string]$ConfigFile,
  [string]$RoksbnkctlExe = 'roksbnkctl',
  [string]$Workspace = 'winctest',
  [string]$Phases = 'cluster,bnk',
  [string]$OutDir = '.\windows-test-out',
  [switch]$TerraformLogDebug
)

# Native stderr from terraform/roksbnkctl must NOT be treated as a throwing error;
# judge every step by its process exit code instead. (PS 'Stop' turns native
# stderr into false failures.)
$ErrorActionPreference = 'Continue'
$ProgressPreference = 'SilentlyContinue'

# ---- output setup ----------------------------------------------------------
$null = New-Item -ItemType Directory -Force -Path $OutDir
$OutDir      = (Resolve-Path $OutDir).Path
$Transcript  = Join-Path $OutDir 'transcript.log'
$TfxCalls    = Join-Path $OutDir 'tfx-calls.log'
$ResultsJson = Join-Path $OutDir 'results.json'
Remove-Item $Transcript, $TfxCalls, $ResultsJson -ErrorAction SilentlyContinue

$script:Checks = New-Object System.Collections.ArrayList

function Write-Log {
  param([string]$Line)
  $stamp = (Get-Date).ToString('HH:mm:ss')
  $msg = "[$stamp] $Line"
  Write-Host $msg
  Add-Content -Path $Transcript -Value $msg -Encoding utf8
}

function Add-Check {
  param([string]$Name, [string]$Status, [string]$Detail = '', [int]$ExitCode = 0, [double]$ElapsedSec = 0)
  $null = $script:Checks.Add([ordered]@{
      name       = $Name
      status     = $Status          # PASS | FAIL | SKIP | INFO
      detail     = $Detail
      exitCode   = $ExitCode
      elapsedSec = [math]::Round($ElapsedSec, 1)
    })
  Write-Log ("CHECK {0,-26} {1}  {2}" -f $Name, $Status, $Detail)
}

# Run a native command, tee merged stdout+stderr to the transcript, return an
# object with .ExitCode / .Elapsed / .Output. Never throws on native stderr.
function Invoke-Native {
  param([string]$Exe, [string[]]$Args)
  Write-Log (">> $Exe $($Args -join ' ')")
  $sw = [System.Diagnostics.Stopwatch]::StartNew()
  $out = & $Exe @Args 2>&1
  $code = $LASTEXITCODE
  $sw.Stop()
  foreach ($o in $out) { Add-Content -Path $Transcript -Value ("   " + ($o | Out-String).TrimEnd()) -Encoding utf8 }
  [pscustomobject]@{ ExitCode = $code; Elapsed = $sw.Elapsed.TotalSeconds; Output = $out }
}

function Test-OnPath { param([string]$Name) [bool](Get-Command $Name -ErrorAction SilentlyContinue) }

# ---- 1. preflight ----------------------------------------------------------
Write-Log '=== PREFLIGHT: native Windows environment ==='

if (-not $env:IBMCLOUD_API_KEY) {
  Add-Check 'ibmcloud_api_key' 'FAIL' 'IBMCLOUD_API_KEY env var is not set'
}
else {
  Add-Check 'ibmcloud_api_key' 'PASS' 'set'
}

foreach ($t in @('terraform', 'helm')) {
  if (Test-OnPath $t) {
    $v = (& $t version 2>&1 | Select-Object -First 1 | Out-String).Trim()
    Add-Check "tool_$t" 'PASS' $v
  }
  else { Add-Check "tool_$t" 'FAIL' "$t not on PATH" }
}

# roksbnkctl.exe resolution
$roksResolved = (Get-Command $RoksbnkctlExe -ErrorAction SilentlyContinue)
if ($roksResolved) {
  $RoksbnkctlExe = $roksResolved.Source
  $rv = (Invoke-Native $RoksbnkctlExe @('version')).Output | Out-String
  Add-Check 'roksbnkctl' 'PASS' ("$RoksbnkctlExe :: " + $rv.Trim())
}
else {
  Add-Check 'roksbnkctl' 'FAIL' "$RoksbnkctlExe not found"
}

# The whole point: prove no Unix shell is on PATH, so success is native.
$shellFound = @()
foreach ($s in @('bash', 'wsl', 'sh', 'busybox')) { if (Test-OnPath $s) { $shellFound += $s } }
if ($shellFound.Count -eq 0) {
  Add-Check 'no_wsl_shell' 'PASS' 'no bash/wsl/sh/busybox on PATH (genuinely native)'
}
else {
  # Not fatal, but it weakens the proof. Record which were found.
  Add-Check 'no_wsl_shell' 'INFO' ("shells present on PATH: " + ($shellFound -join ', ') + " (native-ness not proven; remove from PATH for a clean run)")
}

# Also confirm the classic Unix tools the OLD modules needed are NOT required:
$unixTools = @()
foreach ($u in @('curl', 'grep', 'python3', 'python', 'tr', 'base64')) { if (Test-OnPath $u) { $unixTools += $u } }
Add-Check 'unix_tools_on_path' 'INFO' ($(if ($unixTools) { "present (unused by tfx): " + ($unixTools -join ', ') } else { 'none present' }))

$preflightFailed = @($script:Checks | Where-Object { $_.status -eq 'FAIL' }).Count -gt 0
if ($preflightFailed) {
  Write-Log 'PREFLIGHT FAILED - aborting before any deploy.'
}

# ---- 2. seed workspace -----------------------------------------------------
if (-not $preflightFailed) {
  if (-not (Test-Path $ConfigFile)) {
    Add-Check 'config_file' 'FAIL' "config not found: $ConfigFile"
    $preflightFailed = $true
  }
  else {
    Add-Check 'config_file' 'PASS' (Resolve-Path $ConfigFile).Path
    if ($TerraformLogDebug) { $env:TF_LOG = 'DEBUG' }
    $r = Invoke-Native $RoksbnkctlExe @('-w', $Workspace, 'init', '--config-file', (Resolve-Path $ConfigFile).Path)
    if ($r.ExitCode -eq 0) { Add-Check 'init' 'PASS' 'workspace seeded' 0 $r.Elapsed }
    else { Add-Check 'init' 'FAIL' "init exit $($r.ExitCode)" $r.ExitCode $r.Elapsed; $preflightFailed = $true }
  }
}

# ---- 3. run phases ---------------------------------------------------------
if (-not $preflightFailed) {
  $phaseList = $Phases.Split(',') | ForEach-Object { $_.Trim() } | Where-Object { $_ }
  foreach ($p in $phaseList) {
    Write-Log "=== PHASE: $p up ==="
    $r = Invoke-Native $RoksbnkctlExe @('-w', $Workspace, $p, 'up', '--auto')
    $status = if ($r.ExitCode -eq 0) { 'PASS' } else { 'FAIL' }
    Add-Check "phase_${p}_up" $status "exit $($r.ExitCode)" $r.ExitCode $r.Elapsed
    if ($r.ExitCode -ne 0) {
      Write-Log "Phase $p failed - stopping further phases (leaving state for inspection)."
      break
    }
  }
}

# ---- 4. tfx evidence -------------------------------------------------------
# Pull every tfx invocation and null_resource result out of the transcript so we
# can confirm the Go helpers ran (and see their timings), rather than a shell.
Write-Log '=== tfx evidence extraction ==='
if (Test-Path $Transcript) {
  $tfxLines = Select-String -Path $Transcript -Pattern 'tfx |null_resource\.|cnecontroller_ready|license_active|admission[- ]policy|Creation complete|CNEControllerAvailable|License Active' -SimpleMatch:$false |
  ForEach-Object { $_.Line }
  if ($tfxLines) {
    $tfxLines | Set-Content -Path $TfxCalls -Encoding utf8
    Add-Check 'tfx_evidence' 'PASS' ("$($tfxLines.Count) tfx/null_resource lines -> tfx-calls.log")
  }
  else {
    Add-Check 'tfx_evidence' 'INFO' 'no tfx/null_resource lines captured (phase may not exercise them, or ran before conversion)'
  }
  # A stronger negative signal: if the transcript shows a shell error, native exec broke.
  $shellErr = Select-String -Path $Transcript -Pattern "is not recognized as an internal or external command|'bash'|/bin/bash|No such file or directory|cannot find the path" |
  ForEach-Object { $_.Line } | Select-Object -First 5
  if ($shellErr) {
    Add-Check 'no_shell_errors' 'FAIL' ("shell/exec errors present (see transcript): " + ($shellErr -join ' | '))
  }
  else {
    Add-Check 'no_shell_errors' 'PASS' 'no bash/cmd not-found or path errors in transcript'
  }
}

# ---- 5. emit results.json --------------------------------------------------
$summary = [ordered]@{
  timestampUtc = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
  host         = $env:COMPUTERNAME
  os           = (Get-CimInstance Win32_OperatingSystem -ErrorAction SilentlyContinue).Caption
  psVersion    = $PSVersionTable.PSVersion.ToString()
  workspace    = $Workspace
  phases       = $Phases
  roksbnkctl   = $RoksbnkctlExe
  passed       = @($script:Checks | Where-Object { $_.status -eq 'PASS' }).Count
  failed       = @($script:Checks | Where-Object { $_.status -eq 'FAIL' }).Count
  overall      = $(if (@($script:Checks | Where-Object { $_.status -eq 'FAIL' }).Count -eq 0) { 'PASS' } else { 'FAIL' })
  checks       = @($script:Checks)
}
$summary | ConvertTo-Json -Depth 6 | Set-Content -Path $ResultsJson -Encoding utf8

Write-Log ''
Write-Log ("=== OVERALL: {0}  (passed={1} failed={2}) ===" -f $summary.overall, $summary.passed, $summary.failed)
Write-Log "Artifacts:"
Write-Log "  results.json  : $ResultsJson"
Write-Log "  transcript.log: $Transcript"
Write-Log "  tfx-calls.log : $TfxCalls"

# Non-zero exit on failure so a caller/CI can gate.
if ($summary.overall -eq 'FAIL') { exit 1 } else { exit 0 }
