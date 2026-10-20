# Kyurem M3 end-to-end demo ("a scripted failure is diagnosable") — Windows
# PowerShell twin of m3-dashboard.sh.
#
# Boots the gateway with the in-process span store, sends one failing task
# through it and reads the result back through the management API, which is
# exactly what the dashboard does:
#
#   1. the failing task appears in /api/v1/tasks with status "failed"
#   2. /api/v1/tasks/{id} names the failing span and the error type
#   3. the root-cause panel has a (clearly labelled) heuristic hint
#   4. the overview counts the failure
#
# Requirements: PowerShell 7, go. No Docker, no ClickHouse, no dashboard.
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$Root = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSCommandPath))
Set-Location $Root

$A2AHost   = '127.0.0.1:8080'
$AdminHost = '127.0.0.1:8081'
$MgmtHost  = '127.0.0.1:8082'

$Jobs = @()
$script:Pass = 0
function Pass($msg) { $script:Pass++; Write-Host "PASS: $msg" }
function Fail($msg) { Write-Host "FAIL: $msg" -ForegroundColor Red; exit 1 }
function Note($msg) { Write-Host $msg }

function Wait-Http($url, $name) {
    for ($i = 0; $i -lt 50; $i++) {
        try {
            $r = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 2
            if ($r.StatusCode -eq 200) { return }
        } catch { Start-Sleep -Milliseconds 200 }
    }
    Fail "$name not ready: $url"
}

function Get-Json($url) {
    (Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 10).Content | ConvertFrom-Json
}

try {
    Note '== building binaries =='
    New-Item -ItemType Directory -Force -Path demo/bin | Out-Null
    go build -o demo/bin/kyurem.exe      ./gateway/cmd/kyurem
    if ($LASTEXITCODE -ne 0) { Fail 'build gateway' }
    go build -o demo/bin/demo-client.exe ./demo/scenarios/client
    if ($LASTEXITCODE -ne 0) { Fail 'build demo-client' }

    Note '== validating the shipped config =='
    & demo/bin/kyurem.exe validate --config config/kyurem.example.yaml | Out-Null
    if ($LASTEXITCODE -ne 0) { Fail 'config validate' }

    Note '== booting the gateway (no upstream agents: every call fails) =='
    $Jobs += Start-Process -FilePath 'demo/bin/kyurem.exe' `
        -ArgumentList 'serve --config config/kyurem.example.yaml' -PassThru -WindowStyle Hidden `
        -RedirectStandardOutput 'demo\bin\gateway.log' -RedirectStandardError 'demo\bin\gateway.err.log'

    Wait-Http "http://$AdminHost/healthz" 'gateway admin'
    Wait-Http "http://$MgmtHost/healthz" 'gateway management API'

    Note '-- 1. a failing task reaches the management API --'
    $code = 0
    try {
        Invoke-WebRequest -Uri "http://$A2AHost/a2a/echo" -Method Post -UseBasicParsing -TimeoutSec 10 `
            -Body '{"jsonrpc":"2.0","id":"m3-1","method":"message/send","params":{"message":{"role":"user","parts":[{"kind":"text","text":"hello"}]}}}' `
            -ContentType 'application/json' | Out-Null
    } catch { $code = [int]$_.Exception.Response.StatusCode }
    if ($code -ne 502) { Fail "expected 502 from the unreachable upstream, got $code" }
    Pass 'the unreachable upstream answers 502'

    $tasks = $null
    for ($i = 0; $i -lt 50; $i++) {
        $tasks = Get-Json "http://$MgmtHost/api/v1/tasks?limit=50"
        if ($tasks.items.Count -ge 1) { break }
        Start-Sleep -Milliseconds 200
    }
    if ($tasks.items.Count -lt 1) { Fail 'the failed task did not reach the management API' }
    $task = $tasks.items[0]
    if ($task.status -ne 'failed') { Fail "task status = $($task.status), want failed" }
    Pass 'the task list shows the failed task'
    if (-not $task.error_type) { Fail 'the failed task has no error type' }
    Pass "the task names the failure: $($task.error_type)"

    Note '-- 2. the task detail carries the span tree and the root cause --'
    $detail = Get-Json "http://$MgmtHost/api/v1/tasks/$($task.task_id)"
    if ($detail.spans.Count -lt 1) { Fail 'the detail has no spans' }
    Pass "the detail lists $($detail.spans.Count) span(s)"
    if ($null -eq $detail.root_cause) { Fail 'the detail has no root-cause hint' }
    if ($detail.root_cause.confidence -ne 'heuristic') {
        Fail "the root cause is not labelled as a heuristic: $($detail.root_cause.confidence)"
    }
    if (-not $detail.root_cause.message) { Fail 'the root cause has no message' }
    Pass "root-cause hint: $($detail.root_cause.message)"

    Note '-- 3. the overview counts the failure --'
    $overview = Get-Json "http://$MgmtHost/api/v1/overview"
    if ($overview.tasks_failed -lt 1) { Fail "overview tasks_failed = $($overview.tasks_failed), want >= 1" }
    Pass "overview: $($overview.tasks_total) task(s), $($overview.tasks_failed) failed"

    Note '-- 4. unknown cost stays unknown --'
    $costs = Get-Json "http://$MgmtHost/api/v1/costs?group_by=agent"
    if ($null -ne $costs.total_usd) { Fail "the cost must be unknown, got $($costs.total_usd)" }
    Pass 'the cost is unknown, not zero'

    Write-Host ''
    Write-Host "== $script:Pass assertions passed — M3 demo OK =="
    Write-Host 'The dashboard shows the same data: start it with `make dashboard-build`'
    Write-Host 'and open the task list, or point your browser at the API above.'
} finally {
    foreach ($j in $Jobs) {
        if ($j -and -not $j.HasExited) {
            Stop-Process -Id $j.Id -Force -ErrorAction SilentlyContinue
        }
    }
}
