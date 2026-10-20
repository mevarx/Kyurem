# Kyurem M1 end-to-end demo ("transparent proxy") — Windows/PowerShell twin
# of m1-basic.sh. Builds the gateway, two demo agents and the demo client,
# boots them (echo -> gateway -> upper) and asserts the M1 guarantees:
#
#   1. gateway health: /healthz, /readyz, /version on the admin listener
#   2. discovery: card served through the gateway, URLs rewritten (GW-3)
#   3. plain round trip:  client -> gateway -> echo   (GW-1/GW-2)
#   4. SSE streaming: multiple chunked events relayed live (GW-2)
#   5. alias routing: /a2a/greeter reaches the echo agent (GW-1)
#   6. agent-to-agent chain:  upper -> gateway -> echo (GW-1, go-upper demo)
#   7. unknown agents fail closed with 404 (GW-1)
#
# Requirements: PowerShell 7, go. No Docker needed.
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$Root = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSCommandPath))
Set-Location $Root

# Ports, kept in sync with config/kyurem.example.yaml.
$A2AHost   = '127.0.0.1:8080'
$AdminHost = '127.0.0.1:8081'
$EchoPort  = 9010
$UpperPort = 9011

$Jobs = @()
$script:Pass = 0
function Pass($msg)   { $script:Pass++; Write-Host "PASS: $msg" }
function Fail($msg)   { Write-Host "FAIL: $msg" -ForegroundColor Red; exit 1 }
function Note($msg)   { Write-Host $msg }

function Wait-Http($url, $name) {
    for ($i = 0; $i -lt 50; $i++) {
        try {
            $r = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 2
            if ($r.StatusCode -eq 200) { return }
        } catch { Start-Sleep -Milliseconds 200 }
    }
    Fail "$name not ready: $url"
}

try {
    Note '== building binaries =='
    New-Item -ItemType Directory -Force -Path demo/bin | Out-Null
    go build -o demo/bin/kyurem.exe      ./gateway/cmd/kyurem
    if ($LASTEXITCODE -ne 0) { Fail 'build gateway' }
    go build -o demo/bin/go-echo.exe     ./demo/agents/go-echo
    if ($LASTEXITCODE -ne 0) { Fail 'build go-echo' }
    go build -o demo/bin/go-upper.exe    ./demo/agents/go-upper
    if ($LASTEXITCODE -ne 0) { Fail 'build go-upper' }
    go build -o demo/bin/demo-client.exe ./demo/scenarios/client
    if ($LASTEXITCODE -ne 0) { Fail 'build demo-client' }

    Note '== validating shipped config =='
    & demo/bin/kyurem.exe validate --config config/kyurem.example.yaml
    if ($LASTEXITCODE -ne 0) { Fail 'config validate' }

    Note '== booting: echo -> gateway -> upper =='
    $Jobs += Start-Process -FilePath 'demo/bin/go-echo.exe' -ArgumentList "--port $EchoPort" -PassThru -WindowStyle Hidden
    $Jobs += Start-Process -FilePath 'demo/bin/kyurem.exe' -ArgumentList 'serve --config config/kyurem.example.yaml' -PassThru -WindowStyle Hidden -RedirectStandardOutput 'demo\bin\gateway.log' -RedirectStandardError 'demo\bin\gateway.err.log'
    $echoCard = "http://$A2AHost/a2a/echo/.well-known/agent-card.json"
    $Jobs += Start-Process -FilePath 'demo/bin/go-upper.exe' -ArgumentList "--port $UpperPort --echo-card-url $echoCard" -PassThru -WindowStyle Hidden

    Wait-Http "http://$AdminHost/healthz" 'gateway admin'
    Wait-Http "http://$A2AHost/a2a/echo/.well-known/agent-card.json" 'gateway echo route'
    Wait-Http "http://$A2AHost/a2a/upper/.well-known/agent-card.json" 'gateway upper route'

    Note '-- 1. health endpoints --'
    if ((Invoke-WebRequest -Uri "http://$AdminHost/healthz" -UseBasicParsing).Content -match '"status":"ok"') {
        Pass 'healthz ok'
    } else { Fail 'healthz wrong' }
    if ((Invoke-WebRequest -Uri "http://$AdminHost/readyz" -UseBasicParsing).Content -match '"agents":2') {
        Pass 'readyz lists 2 agents'
    } else { Fail 'readyz wrong' }
    if ((Invoke-WebRequest -Uri "http://$AdminHost/version" -UseBasicParsing).Content -match 'a2a_spec_version') {
        Pass 'version reports spec version'
    } else { Fail 'version wrong' }

    Note '-- 2. discovery: card through the gateway is rewritten --'
    $card = (Invoke-WebRequest -Uri "http://$A2AHost/a2a/echo/.well-known/agent-card.json" -UseBasicParsing).Content
    if (-not $card.Contains('"name":"go-echo"')) { Fail "card name missing: $card" }
    if (-not $card.Contains('"protocolBinding":"JSONRPC"')) { Fail "card binding missing: $card" }
    if ($card.Contains('"url":"http://' + $A2AHost + '/a2a/echo"')) {
        Pass 'card interface URL rewritten to the gateway'
    } else {
        Fail "card URL not rewritten: $card"
    }

    Note '-- 3. plain round trip through the gateway --'
    $out = & demo/bin/demo-client.exe --gateway-url "http://$A2AHost/a2a/echo" --message 'hello gateway'
    if ($LASTEXITCODE -ne 0) { Fail "demo-client failed: $($out -join ' ')" }
    if (($out -join "`n").Contains('RESULT: echo: hello gateway')) { Pass 'echo round trip result correct' }
    else { Fail "unexpected: $($out -join ' ')" }

    Note '-- 4. SSE streaming through the gateway --'
    $out = & demo/bin/demo-client.exe --gateway-url "http://$A2AHost/a2a/echo" --message 'hello gateway' --stream (5s)
    if ($LASTEXITCODE -ne 0) { Fail "demo-client stream failed: $($out -join ' ')" }
    $joined = $out -join "`n"
    if (-not $joined.Contains('chunk:echo: hello')) { Fail "first chunk missing: $joined" }
    if (-not $joined.Contains('TASK_STATE_COMPLETED')) { Fail "completed status event missing: $joined" }
    if ($joined.Contains('RESULT: echo: hello gateway')) { Pass 'streamed chunks reassemble to the echo reply' }
    else { Fail "stream result wrong: $joined" }

    Note '-- 5. alias routing --'
    $out = & demo/bin/demo-client.exe --gateway-url "http://$A2AHost/a2a/greeter" --message 'alias check'
    if ($LASTEXITCODE -ne 0) { Fail "demo-client alias failed: $($out -join ' ')" }
    if (($out -join "`n").Contains('RESULT: echo: alias check')) { Pass 'alias /a2a/greeter reaches echo' }
    else { Fail "unexpected: $($out -join ' ')" }

    Note '-- 6. agent-to-agent chain: upper -> gateway -> echo --'
    $out = & demo/bin/demo-client.exe --gateway-url "http://$A2AHost/a2a/upper" --message 'hello gateway'
    if ($LASTEXITCODE -ne 0) { Fail "demo-client upper failed: $($out -join ' ')" }
    if (($out -join "`n").Contains('RESULT: upper: HELLO GATEWAY')) { Pass 'chained round trip (marker proves it went through echo)' }
    else { Fail "unexpected: $($out -join ' ')" }

    Note '-- 7. unknown agent fails closed --'
    $code = 0
    try {
        Invoke-WebRequest -Uri "http://$A2AHost/a2a/does-not-exist" -UseBasicParsing -TimeoutSec 5 | Out-Null
    } catch { $code = [int]$_.Exception.Response.StatusCode }
    if ($code -eq 404) { Pass 'unknown agent -> 404' } else { Fail "unknown agent -> HTTP $code" }

    Write-Host ''
    Write-Host "== $script:Pass assertions passed — M1 demo OK =="
} finally {
    foreach ($j in $Jobs) {
        if ($j -and -not $j.HasExited) {
            Stop-Process -Id $j.Id -Force -ErrorAction SilentlyContinue
        }
    }
}
